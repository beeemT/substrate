ALTER TABLE agent_sessions ADD COLUMN resume_info TEXT;
UPDATE agent_sessions
    SET resume_info = json_object(
        'omp_session_file', omp_session_file,
        'omp_session_id', omp_session_id
    )
    WHERE omp_session_file IS NOT NULL;
ALTER TABLE agent_sessions DROP COLUMN omp_session_file;
ALTER TABLE agent_sessions DROP COLUMN omp_session_id;
