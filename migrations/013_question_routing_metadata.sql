ALTER TABLE questions ADD COLUMN stage TEXT;
ALTER TABLE questions ADD COLUMN source TEXT;
ALTER TABLE questions ADD COLUMN structured TEXT;
ALTER TABLE questions ADD COLUMN answer_data TEXT;

UPDATE questions
SET stage = 'implementation'
WHERE stage IS NULL OR stage = '';

UPDATE questions
SET source = 'ask_foreman'
WHERE source IS NULL OR source = '';
