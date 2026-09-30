// Package inbox 定义至少一次消息消费时使用的通用幂等契约。
package inbox

import "errors"

// ErrMessageConflict 表示同一个消息 ID 被不同 Topic 或 Payload 重复使用。
// 这不是普通的重复投递，而是生产者违反了消息 ID 的唯一性约定。
var ErrMessageConflict = errors.New("inbox message ID was reused with different content")
