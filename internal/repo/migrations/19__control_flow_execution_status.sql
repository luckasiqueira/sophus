-- +goose Up
UPDATE conversations SET status = 'closed' WHERE status = 'resolved';

WITH duplicated AS (
    SELECT id,
           row_number() OVER (PARTITION BY "conversationId" ORDER BY id DESC) AS position
    FROM flow_executions
    WHERE status IN ('running', 'waiting')
)
UPDATE flow_executions e
SET status = 'failed',
    "errorMessage" = 'execução concorrente encerrada durante migração',
    "completedAt" = now(),
    "updatedAt" = now()
FROM duplicated d
WHERE e.id = d.id AND d.position > 1;

UPDATE conversations c
SET status = 'running', "updatedAt" = now()
WHERE c.status = 'open'
  AND EXISTS (
      SELECT 1 FROM flow_executions e
      WHERE e."conversationId" = c.id AND e.status IN ('running', 'waiting')
  );

UPDATE conversations c
SET status = 'open', "updatedAt" = now()
WHERE c.status = 'running'
  AND NOT EXISTS (
      SELECT 1 FROM flow_executions e
      WHERE e."conversationId" = c.id AND e.status IN ('running', 'waiting')
  );

CREATE UNIQUE INDEX flow_executions_one_active_per_conversation
    ON flow_executions ("conversationId")
    WHERE status IN ('running', 'waiting');

-- +goose Down
DROP INDEX IF EXISTS flow_executions_one_active_per_conversation;
