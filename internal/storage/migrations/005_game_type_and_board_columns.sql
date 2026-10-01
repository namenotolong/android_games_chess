ALTER TABLE rooms ADD COLUMN game_type TEXT NOT NULL DEFAULT 'GOMOKU';
ALTER TABLE rooms ADD COLUMN board_columns INTEGER NOT NULL DEFAULT 15;
CREATE INDEX idx_rooms_game_type_code ON rooms(game_type, room_code);
