// Package clock 提供时间来源的具体实现。
package clock

import "time"

// System 返回操作系统当前时间。应用层依赖 Clock 接口，测试时可以
// 换成固定时间，避免测试结果随运行时刻变化。
type System struct{}

func (System) Now() time.Time {
	return time.Now().UTC()
}
