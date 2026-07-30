ALTER TABLE jobs
    ADD COLUMN required_tool_ids_json TEXT NOT NULL DEFAULT '[]'
    CHECK (length(required_tool_ids_json) BETWEEN 2 AND 8192);
