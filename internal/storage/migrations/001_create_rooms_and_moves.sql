CREATE TABLE rooms (
    room_id TEXT PRIMARY KEY,
    room_code TEXT NOT NULL UNIQUE,
    host_player_id TEXT NOT NULL,
    guest_player_id TEXT,
    status TEXT NOT NULL CHECK (status IN ('WAITING_FOR_OPPONENT', 'READY', 'IN_PROGRESS', 'FINISHED', 'CLOSED')),
    board_size INTEGER NOT NULL DEFAULT 15 CHECK (board_size >= 5),
    board_json TEXT NOT NULL DEFAULT '[]',
    current_piece TEXT NOT NULL DEFAULT 'BLACK' CHECK (current_piece IN ('BLACK', 'WHITE')),
    move_count INTEGER NOT NULL DEFAULT 0 CHECK (move_count >= 0),
    revision INTEGER NOT NULL DEFAULT 0 CHECK (revision >= 0),
    winner_player_id TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    expires_at TEXT
);

CREATE INDEX idx_rooms_status_expires_at ON rooms(status, expires_at);
