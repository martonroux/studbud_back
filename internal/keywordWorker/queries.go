package keywordWorker

const sqlEnqueueJob = `
INSERT INTO ai_extraction_jobs (fc_id, priority, state)
VALUES ($1, $2, 'pending')
ON CONFLICT (fc_id) WHERE state IN ('pending','running')
DO UPDATE SET
  priority   = GREATEST(ai_extraction_jobs.priority, EXCLUDED.priority),
  updated_at = now()
`

const sqlClaimPending = `
UPDATE ai_extraction_jobs
SET state = 'running', started_at = now(), updated_at = now()
WHERE id IN (
  SELECT id FROM ai_extraction_jobs
  WHERE state = 'pending'
  ORDER BY priority DESC, enqueued_at ASC
  FOR UPDATE SKIP LOCKED
  LIMIT $1
)
RETURNING id, fc_id, attempts
`

const sqlMarkDone = `
UPDATE ai_extraction_jobs
SET state = 'done', finished_at = now(), updated_at = now()
WHERE id = $1
`

const sqlMarkFailed = `
UPDATE ai_extraction_jobs
SET state = 'failed', last_error = $2, finished_at = now(), updated_at = now()
WHERE id = $1
`

const sqlRequeueAfterError = `
UPDATE ai_extraction_jobs
SET state = 'pending', priority = 0, attempts = attempts + 1,
    last_error = $2, updated_at = now(), started_at = NULL
WHERE id = $1
`

const sqlReplaceKeywordsDelete = `DELETE FROM flashcard_keywords WHERE fc_id = $1`

const sqlReplaceKeywordsInsert = `
INSERT INTO flashcard_keywords (fc_id, keyword, weight) VALUES ($1, $2, $3)
`

const sqlReapStuckRunning = `
UPDATE ai_extraction_jobs
SET state = 'pending', attempts = attempts + 1,
    last_error = 'reaped: running > 5m', updated_at = now(), started_at = NULL
WHERE state = 'running' AND started_at < now() - interval '5 minutes'
`

const sqlBackfillExisting = `
INSERT INTO ai_extraction_jobs (fc_id, priority, state)
SELECT id, -1, 'pending' FROM flashcards
ON CONFLICT (fc_id) WHERE state IN ('pending','running') DO NOTHING
`
