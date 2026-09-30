ALTER TABLE rooms ADD COLUMN host_player_name TEXT NOT NULL DEFAULT '玩家';
ALTER TABLE rooms ADD COLUMN guest_player_name TEXT;
ALTER TABLE rooms ADD COLUMN host_token_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE rooms ADD COLUMN guest_token_hash TEXT;
ALTER TABLE rooms ADD COLUMN result TEXT;
