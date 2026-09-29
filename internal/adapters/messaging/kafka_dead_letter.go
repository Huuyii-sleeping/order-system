package messaging

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	deadLetterHeaderPrefix  = "order-system-dlq-"
	originalTopicHeader     = deadLetterHeaderPrefix + "original-topic"
	originalPartitionHeader = deadLetterHeaderPrefix + "original-partition"
	originalOffsetHeader    = deadLetterHeaderPrefix + "original-offset"
	consumerGroupHeader     = deadLetterHeaderPrefix + "consumer-group"
	deliveryAttemptsHeader  = deadLetterHeaderPrefix + "delivery-attempts"
	failedAtHeader          = deadLetterHeaderPrefix + "failed-at"
	processingErrorHeader   = deadLetterHeaderPrefix + "processing-error"
	maxDeadLetterErrorRunes = 1000
)

// deadLetterRecord 保留无法处理的原始 Key、Value 和业务信封，并追加可审计的
// 失败位置与原因。原 Topic/Partition/Offset 构成源记录的稳定身份，可用于重放去重。
func deadLetterRecord(
	original *kgo.Record, topic, groupID string, attempts int, processingErr error, failedAt time.Time,
) (*kgo.Record, error) {
	if original == nil {
		return nil, errors.New("create kafka dead letter: nil original record")
	}
	topic = strings.TrimSpace(topic)
	groupID = strings.TrimSpace(groupID)
	if topic == "" || groupID == "" || attempts <= 0 || processingErr == nil {
		return nil, errors.New("create kafka dead letter: invalid failure metadata")
	}

	headers := make([]kgo.RecordHeader, 0, len(original.Headers)+7)
	for _, header := range original.Headers {
		// 不信任来源消息里伪造的 DLQ 元数据，只保留业务信封。
		if !strings.HasPrefix(header.Key, deadLetterHeaderPrefix) {
			headers = append(headers, kgo.RecordHeader{
				Key: header.Key, Value: append([]byte(nil), header.Value...),
			})
		}
	}
	headers = append(headers,
		kgo.RecordHeader{Key: originalTopicHeader, Value: []byte(original.Topic)},
		kgo.RecordHeader{Key: originalPartitionHeader, Value: []byte(strconv.FormatInt(int64(original.Partition), 10))},
		kgo.RecordHeader{Key: originalOffsetHeader, Value: []byte(strconv.FormatInt(original.Offset, 10))},
		kgo.RecordHeader{Key: consumerGroupHeader, Value: []byte(groupID)},
		kgo.RecordHeader{Key: deliveryAttemptsHeader, Value: []byte(strconv.Itoa(attempts))},
		kgo.RecordHeader{Key: failedAtHeader, Value: []byte(failedAt.UTC().Format(time.RFC3339Nano))},
		kgo.RecordHeader{Key: processingErrorHeader, Value: []byte(truncateDeadLetterError(processingErr.Error()))},
	)

	return &kgo.Record{
		Topic: topic, Key: append([]byte(nil), original.Key...), Value: append([]byte(nil), original.Value...),
		Headers: headers, Timestamp: failedAt.UTC(),
	}, nil
}

func truncateDeadLetterError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "unknown processing error"
	}
	runes := []rune(value)
	if len(runes) > maxDeadLetterErrorRunes {
		return fmt.Sprintf("%s...", string(runes[:maxDeadLetterErrorRunes]))
	}
	return value
}
