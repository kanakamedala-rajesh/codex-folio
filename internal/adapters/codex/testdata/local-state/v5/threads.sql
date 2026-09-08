CREATE TABLE threads (
    id TEXT PRIMARY KEY,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL,
    source TEXT NOT NULL,
    model TEXT,
    cwd TEXT NOT NULL,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL,
    preview TEXT NOT NULL,
    first_user_message TEXT NOT NULL,
    future_unknown TEXT
);

INSERT INTO threads (id, created_at_ms, updated_at_ms, source, model, cwd, tokens_used, title, preview, first_user_message, future_unknown) VALUES
('018f4f70-6f77-7c3f-9b77-93aa087dfc4d', 1788696000000, 1788696300000, 'cli', 'gpt-5', '/private/repository', 42, 'private title', 'private preview', 'private prompt', 'credential sentinel'),
('018f4f70-6f77-7c3f-9b77-93aa087dfc4e', 1788696600000, 1788696600000, 'cli', NULL, '/private/other', 0, 'other title', 'other preview', 'other prompt', 'future sentinel');
