package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
)

// BackgroundTask 是一个与 HTTP Server 共同存活的后台组件。stopCtx 用于停止
// 领取新任务，workCtx 用于在关闭预算耗尽时取消已经开始的工作。
type BackgroundTask struct {
	Name string
	Run  func(stopCtx, workCtx context.Context) error
}

// BackgroundRuntime 统一管理 Outbox、Payment 和消息 Consumer 的生命周期。
// 任一组件意外退出都会通知主进程，避免 API 看似健康但后台链路已经停止。
type BackgroundRuntime struct {
	stopTaking context.CancelFunc
	cancelWork context.CancelFunc
	done       chan struct{}
	errors     chan error
	stopOnce   sync.Once
	cancelOnce sync.Once
}

func StartBackgroundRuntime(logger *slog.Logger, tasks ...BackgroundTask) *BackgroundRuntime {
	stopCtx, stopTaking := context.WithCancel(context.Background())
	workCtx, cancelWork := context.WithCancel(context.Background())
	runtime := &BackgroundRuntime{
		stopTaking: stopTaking, cancelWork: cancelWork,
		done: make(chan struct{}), errors: make(chan error, len(tasks)),
	}

	var workers sync.WaitGroup
	workers.Add(len(tasks))
	for _, task := range tasks {
		task := task
		go func() {
			defer workers.Done()
			err := runBackgroundTask(task, stopCtx, workCtx)
			if err == nil && stopCtx.Err() == nil && workCtx.Err() == nil {
				err = errors.New("returned before shutdown")
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				wrapped := fmt.Errorf("background task %q stopped: %w", task.Name, err)
				select {
				case runtime.errors <- wrapped:
				default:
				}
				if logger != nil {
					logger.Error("background task stopped unexpectedly",
						"component", task.Name, "error", err,
					)
				}
			}
		}()
	}
	go func() {
		workers.Wait()
		close(runtime.done)
	}()
	return runtime
}

func runBackgroundTask(
	task BackgroundTask, stopCtx, workCtx context.Context,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("panic: %v\n%s", recovered, debug.Stack())
		}
	}()
	if task.Name == "" || task.Run == nil {
		return errors.New("background task requires a name and runner")
	}
	return task.Run(stopCtx, workCtx)
}

// BeginDrain 停止所有后台组件领取新任务，但不取消已经开始的工作。
func (r *BackgroundRuntime) BeginDrain() {
	if r != nil {
		r.stopOnce.Do(r.stopTaking)
	}
}

// ForceStop 同时取消领取和在途工作，用于优雅关闭预算已经耗尽的情况。
func (r *BackgroundRuntime) ForceStop() {
	if r == nil {
		return
	}
	r.BeginDrain()
	r.cancelOnce.Do(r.cancelWork)
}

func (r *BackgroundRuntime) Errors() <-chan error {
	if r == nil {
		return nil
	}
	return r.errors
}

func (r *BackgroundRuntime) Wait(ctx context.Context) error {
	if r == nil {
		return nil
	}
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
