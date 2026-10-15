CREATE TABLE message_recovery_actions (
    id TEXT PRIMARY KEY,
    action_type TEXT NOT NULL CHECK (action_type IN ('outbox_retry', 'dlq_replay')),
    target TEXT NOT NULL CHECK (length(trim(target)) BETWEEN 1 AND 500),
    actor TEXT NOT NULL CHECK (length(trim(actor)) BETWEEN 1 AND 200),
    reason TEXT NOT NULL CHECK (length(trim(reason)) BETWEEN 1 AND 1000),
    status TEXT NOT NULL CHECK (status IN ('requested', 'completed', 'unknown')),
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    CHECK (
        (status = 'requested' AND completed_at IS NULL AND last_error IS NULL)
        OR (status = 'completed' AND completed_at IS NOT NULL AND last_error IS NULL)
        OR (status = 'unknown' AND completed_at IS NOT NULL AND last_error IS NOT NULL)
    )
);

-- 同一个 DLQ 坐标只允许进入一次受控重放流程。结果不确定时不能靠再次执行
-- 命令盲目重发，必须先根据稳定 message_id 检查 Inbox 和业务状态。
CREATE UNIQUE INDEX message_recovery_dlq_target_unique
    ON message_recovery_actions (action_type, target)
    WHERE action_type = 'dlq_replay';

CREATE INDEX message_recovery_actions_created_at_idx
    ON message_recovery_actions (created_at DESC, id DESC);
