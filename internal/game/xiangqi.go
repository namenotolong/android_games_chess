package game

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

type XiangqiMove struct {
	FromRow    int `json:"fromRow"`
	FromColumn int `json:"fromColumn"`
	ToRow      int `json:"toRow"`
	ToColumn   int `json:"toColumn"`
}
type XiangqiSnapshot struct {
	RoomID        string       `json:"roomId"`
	RoomCode      string       `json:"roomCode"`
	HostPlayerID  string       `json:"hostPlayerId"`
	HostName      string       `json:"hostName"`
	GuestPlayerID *string      `json:"guestPlayerId"`
	GuestName     *string      `json:"guestName"`
	Status        string       `json:"status"`
	Board         []string     `json:"board"`
	CurrentTurn   string       `json:"currentTurn"`
	MoveCount     int          `json:"moveCount"`
	Revision      int64        `json:"revision"`
	Winner        *string      `json:"winner"`
	LastMove      *XiangqiMove `json:"lastMove"`
}
type XiangqiSession struct {
	RoomID      string          `json:"roomId"`
	RoomCode    string          `json:"roomCode"`
	PlayerID    string          `json:"playerId"`
	PlayerToken string          `json:"playerToken"`
	LocalSide   string          `json:"localSide"`
	Match       XiangqiSnapshot `json:"match"`
}

func (s *Service) CreateXiangqi(ctx context.Context, name string) (XiangqiSession, error) {
	pid, err := newID()
	if err != nil {
		return XiangqiSession{}, err
	}
	token, err := randomToken()
	if err != nil {
		return XiangqiSession{}, err
	}
	seed := make([]byte, 4)
	if _, err = rand.Read(seed); err != nil {
		return XiangqiSession{}, err
	}
	start := int(seed[0])<<24 | int(seed[1])<<16 | int(seed[2])<<8 | int(seed[3])
	now := time.Now().UTC().Format(time.RFC3339Nano)
	board := initialXiangqi()
	encoded, _ := json.Marshal(board)
	for i := 0; i < 10000; i++ {
		id, e := newID()
		if e != nil {
			return XiangqiSession{}, e
		}
		code := roomCode((start + i) % 10000)
		_, e = s.db.ExecContext(ctx, "INSERT INTO rooms(room_id,room_code,host_player_id,status,board_size,board_columns,board_json,current_piece,created_at,updated_at,host_player_name,host_token_hash,host_piece,guest_piece,last_move_json,undo_snapshot_json,game_type) VALUES(?,?,?,'WAITING_FOR_OPPONENT',10,9,?,'WHITE',?,?,?,?,'BLACK','WHITE','null','null','XIANGQI')", id, code, pid, string(encoded), now, now, cleanName(name), hashToken(token))
		if e == nil {
			snap, e := s.xiangqiSnapshot(ctx, id)
			return XiangqiSession{id, code, pid, token, "RED", snap}, e
		}
		if !isRoomCodeCollision(e) {
			return XiangqiSession{}, e
		}
	}
	return XiangqiSession{}, ErrRoomCapacity
}
func (s *Service) JoinXiangqi(ctx context.Context, code, name string) (XiangqiSession, error) {
	var id, status string
	e := s.db.QueryRowContext(ctx, "SELECT room_id,status FROM rooms WHERE game_type='XIANGQI' AND room_code=?", strings.TrimSpace(code)).Scan(&id, &status)
	if e != nil {
		return XiangqiSession{}, ErrNotFound
	}
	if status != "WAITING_FOR_OPPONENT" {
		return XiangqiSession{}, ErrConflict
	}
	pid, e := newID()
	if e != nil {
		return XiangqiSession{}, e
	}
	token, e := randomToken()
	if e != nil {
		return XiangqiSession{}, e
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	r, e := s.db.ExecContext(ctx, "UPDATE rooms SET guest_player_id=?,guest_player_name=?,guest_token_hash=?,status='IN_PROGRESS',updated_at=? WHERE room_id=? AND game_type='XIANGQI' AND status='WAITING_FOR_OPPONENT'", pid, cleanName(name), hashToken(token), now, id)
	if e != nil {
		return XiangqiSession{}, e
	}
	n, _ := r.RowsAffected()
	if n != 1 {
		return XiangqiSession{}, ErrConflict
	}
	snap, e := s.xiangqiSnapshot(ctx, id)
	return XiangqiSession{id, snap.RoomCode, pid, token, "BLACK", snap}, e
}
func (s *Service) xiangqiSnapshot(ctx context.Context, id string) (XiangqiSnapshot, error) {
	var x XiangqiSnapshot
	var guest, guestName, winner, board, last sql.NullString
	e := s.db.QueryRowContext(ctx, "SELECT room_id,room_code,host_player_id,host_player_name,guest_player_id,guest_player_name,status,board_json,current_piece,move_count,revision,winner_player_id,last_move_json FROM rooms WHERE room_id=? AND game_type='XIANGQI'", id).Scan(&x.RoomID, &x.RoomCode, &x.HostPlayerID, &x.HostName, &guest, &guestName, &x.Status, &board, &x.CurrentTurn, &x.MoveCount, &x.Revision, &winner, &last)
	if e != nil {
		return x, ErrNotFound
	}
	if x.CurrentTurn == "WHITE" {
		x.CurrentTurn = "RED"
	} else {
		x.CurrentTurn = "BLACK"
	}
	_ = json.Unmarshal([]byte(board.String), &x.Board)
	if x.Board == nil {
		x.Board = initialXiangqi()
	}
	if guest.Valid {
		x.GuestPlayerID = &guest.String
	}
	if guestName.Valid {
		x.GuestName = &guestName.String
	}
	if winner.Valid {
		winningSide := "BLACK"
		if winner.String == x.HostPlayerID {
			winningSide = "RED"
		}
		x.Winner = &winningSide
	}
	if last.Valid && last.String != "null" {
		var m XiangqiMove
		if json.Unmarshal([]byte(last.String), &m) == nil {
			x.LastMove = &m
		}
	}
	return x, nil
}
func (s *Service) AuthorizedXiangqi(ctx context.Context, id, token string) (XiangqiSnapshot, string, error) {
	var pid string
	e := s.db.QueryRowContext(ctx, "SELECT host_player_id FROM rooms WHERE room_id=? AND game_type='XIANGQI' AND host_token_hash=? UNION ALL SELECT guest_player_id FROM rooms WHERE room_id=? AND game_type='XIANGQI' AND guest_token_hash=? LIMIT 1", id, hashToken(token), id, hashToken(token)).Scan(&pid)
	if e != nil {
		return XiangqiSnapshot{}, "", ErrNotFound
	}
	x, e := s.xiangqiSnapshot(ctx, id)
	return x, pid, e
}
func (s *Service) PlayXiangqi(ctx context.Context, id, token string, revision int64, m XiangqiMove) (XiangqiSnapshot, error) {
	x, pid, e := s.AuthorizedXiangqi(ctx, id, token)
	if e != nil {
		return x, e
	}
	if x.Revision != revision || x.Status != "IN_PROGRESS" {
		return x, ErrConflict
	}
	side := "BLACK"
	if pid == x.HostPlayerID {
		side = "RED"
	}
	if side != x.CurrentTurn {
		return x, ErrInvalid
	}
	if m.FromRow < 0 || m.FromRow >= 10 || m.ToRow < 0 || m.ToRow >= 10 || m.FromColumn < 0 || m.FromColumn >= 9 || m.ToColumn < 0 || m.ToColumn >= 9 {
		return x, ErrInvalid
	}
	idx := func(r, c int) int { return r*9 + c }
	piece := x.Board[idx(m.FromRow, m.FromColumn)]
	if !strings.HasPrefix(piece, side+"_") || piece == "" || !validXiangqiMove(x.Board, side, m) {
		return x, ErrInvalid
	}
	captured := x.Board[idx(m.ToRow, m.ToColumn)]
	x.Board[idx(m.ToRow, m.ToColumn)] = piece
	x.Board[idx(m.FromRow, m.FromColumn)] = ""
	if xiangqiCheck(x.Board, side) {
		return x, ErrInvalid
	}
	x.MoveCount++
	next := "RED"
	if side == "RED" {
		next = "BLACK"
	}
	x.CurrentTurn = next
	x.LastMove = &m
	if captured == "BLACK_GENERAL" || captured == "RED_GENERAL" || !hasXiangqiMove(x.Board, next) {
		x.Status = "FINISHED"
		x.Winner = &side
	}
	b, _ := json.Marshal(x.Board)
	lm, _ := json.Marshal(m)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	dbTurn := "BLACK"
	if x.CurrentTurn == "RED" {
		dbTurn = "WHITE"
	}
	res, e := s.db.ExecContext(ctx, "UPDATE rooms SET board_json=?,current_piece=?,move_count=?,revision=revision+1,status=?,winner_player_id=?,last_move_json=?,updated_at=? WHERE room_id=? AND game_type='XIANGQI' AND revision=?", string(b), dbTurn, x.MoveCount, x.Status, func() any {
		if x.Winner != nil {
			return playerForSide(x, *x.Winner)
		}
		return nil
	}(), string(lm), now, id, revision)
	if e != nil {
		return x, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return x, ErrConflict
	}
	return s.xiangqiSnapshot(ctx, id)
}
func playerForSide(x XiangqiSnapshot, side string) string {
	if side == "RED" {
		return x.HostPlayerID
	}
	if x.GuestPlayerID != nil {
		return *x.GuestPlayerID
	}
	return ""
}
func (s *Service) XiangqiAction(ctx context.Context, id, token, action string, revision int64) (XiangqiSnapshot, error) {
	x, pid, e := s.AuthorizedXiangqi(ctx, id, token)
	if e != nil {
		return x, e
	}
	if x.Revision != revision {
		return x, ErrConflict
	}
	if action == "resign" {
		side := "RED"
		if pid == x.HostPlayerID {
			side = "BLACK"
		}
		wp := playerForSide(x, side)
		_, e = s.db.ExecContext(ctx, "UPDATE rooms SET status='FINISHED',winner_player_id=?,result='RESIGN',revision=revision+1,updated_at=? WHERE room_id=? AND revision=?", wp, time.Now().UTC().Format(time.RFC3339Nano), id, revision)
		if e != nil {
			return x, e
		}
		return s.xiangqiSnapshot(ctx, id)
	}
	if action != "restart" {
		return x, ErrInvalid
	}
	board, _ := json.Marshal(initialXiangqi())
	_, e = s.db.ExecContext(ctx, "UPDATE rooms SET board_json=?,current_piece='WHITE',move_count=0,status=CASE WHEN guest_player_id IS NULL THEN 'WAITING_FOR_OPPONENT' ELSE 'IN_PROGRESS' END,winner_player_id=NULL,result=NULL,last_move_json='null',revision=revision+1,updated_at=? WHERE room_id=? AND revision=?", string(board), time.Now().UTC().Format(time.RFC3339Nano), id, revision)
	if e != nil {
		return x, e
	}
	return s.xiangqiSnapshot(ctx, id)
}
func initialXiangqi() []string {
	b := make([]string, 90)
	rank := []string{"CHARIOT", "HORSE", "ELEPHANT", "ADVISOR", "GENERAL", "ADVISOR", "ELEPHANT", "HORSE", "CHARIOT"}
	for c, t := range rank {
		b[c] = "BLACK_" + t
		b[81+c] = "RED_" + t
	}
	b[19] = "BLACK_CANNON"
	b[25] = "BLACK_CANNON"
	b[64] = "RED_CANNON"
	b[70] = "RED_CANNON"
	for c := 0; c < 9; c += 2 {
		b[27+c] = "BLACK_SOLDIER"
		b[54+c] = "RED_SOLDIER"
	}
	return b
}
func hasXiangqiMove(b []string, side string) bool {
	for i, p := range b {
		if strings.HasPrefix(p, side+"_") {
			for j := 0; j < 90; j++ {
				m := XiangqiMove{i / 9, i % 9, j / 9, j % 9}
				if validXiangqiMove(b, side, m) {
					n := append([]string(nil), b...)
					n[j] = n[i]
					n[i] = ""
					if !xiangqiCheck(n, side) {
						return true
					}
				}
			}
		}
	}
	return false
}
func validXiangqiMove(b []string, side string, m XiangqiMove) bool {
	fr, fc, tr, tc := m.FromRow, m.FromColumn, m.ToRow, m.ToColumn
	p := b[fr*9+fc]
	target := b[tr*9+tc]
	if target != "" && strings.HasPrefix(target, side+"_") {
		return false
	}
	dr, dc := tr-fr, tc-fc
	adr, adc := abs(dr), abs(dc)
	count := 0
	for r, c := fr+sign(dr), fc+sign(dc); r != tr || c != tc; r, c = r+sign(dr), c+sign(dc) {
		if r >= 0 && r < 10 && c >= 0 && c < 9 && b[r*9+c] != "" {
			count++
		}
	}
	switch strings.TrimPrefix(p, side+"_") {
	case "GENERAL":
		if target == map[bool]string{true: "BLACK_GENERAL", false: "RED_GENERAL"}[side == "RED"] {
			return fc == tc && count == 0
		}
		return adr+adc == 1 && tc >= 3 && tc <= 5 && ((side == "RED" && tr >= 7) || (side == "BLACK" && tr <= 2))
	case "ADVISOR":
		return adr == 1 && adc == 1 && tc >= 3 && tc <= 5 && ((side == "RED" && tr >= 7) || (side == "BLACK" && tr <= 2))
	case "ELEPHANT":
		return adr == 2 && adc == 2 && ((side == "RED" && tr >= 5) || (side == "BLACK" && tr <= 4)) && b[(fr+dr/2)*9+fc+dc/2] == ""
	case "HORSE":
		return (adr == 2 && adc == 1 && b[(fr+sign(dr))*9+fc] == "") || (adr == 1 && adc == 2 && b[fr*9+fc+sign(dc)] == "")
	case "CHARIOT":
		return (dr == 0 || dc == 0) && count == 0
	case "CANNON":
		if dr != 0 && dc != 0 {
			return false
		}
		if target == "" {
			return count == 0
		}
		return count == 1
	case "SOLDIER":
		forward := -1
		if side == "BLACK" {
			forward = 1
		}
		if dr == forward && dc == 0 {
			return true
		}
		crossed := side == "RED" && fr <= 4 || side == "BLACK" && fr >= 5
		return crossed && dr == 0 && adc == 1
	}
	return false
}
func xiangqiCheck(b []string, side string) bool {
	g := -1
	for i, p := range b {
		if p == side+"_GENERAL" {
			g = i
			break
		}
	}
	if g < 0 {
		return true
	}
	enemy := "RED"
	if side == "RED" {
		enemy = "BLACK"
	}
	for i, p := range b {
		if strings.HasPrefix(p, enemy+"_") {
			m := XiangqiMove{i / 9, i % 9, g / 9, g % 9}
			if validXiangqiMove(b, enemy, m) {
				return true
			}
		}
	}
	return false
}
func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
func sign(v int) int {
	if v < 0 {
		return -1
	}
	if v > 0 {
		return 1
	}
	return 0
}
