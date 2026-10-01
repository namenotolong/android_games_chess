package game

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mattn/go-sqlite3"
)

var ErrNotFound = errors.New("房间不存在或访问令牌无效")
var ErrConflict = errors.New("房间状态已变化，请刷新后重试")
var ErrInvalid = errors.New("当前不能在该位置落子")
var ErrRoomCapacity = errors.New("线上人数过多，创建失败，请稍后再试")
var ErrSnapshotCorrupt = errors.New("棋盘快照异常，请重开本局")

type Service struct{ db *sql.DB }
type PendingAction struct {
	Type                string `json:"type"`
	RequestedByPlayerID string `json:"requestedByPlayerId"`
}
type Snapshot struct {
	RoomID            string         `json:"roomId"`
	RoomCode          string         `json:"roomCode"`
	HostPlayerID      string         `json:"hostPlayerId"`
	HostName          string         `json:"hostName"`
	GuestPlayerID     *string        `json:"guestPlayerId"`
	GuestName         *string        `json:"guestName"`
	HostPiece         string         `json:"hostPiece"`
	GuestPiece        string         `json:"guestPiece"`
	Status            string         `json:"status"`
	BoardSize         int            `json:"boardSize"`
	Board             []string       `json:"board"`
	CurrentPiece      string         `json:"currentPiece"`
	MoveCount         int            `json:"moveCount"`
	Revision          int64          `json:"revision"`
	WinnerPlayerID    *string        `json:"winnerPlayerId"`
	Result            *string        `json:"result"`
	LastMove          *MoveSnapshot  `json:"lastMove"`
	RecentMovePlayers []string       `json:"recentMovePlayers"`
	CanUndo           bool           `json:"canUndo"`
	PendingAction     *PendingAction `json:"pendingAction"`
	undoSnapshot      string
}
type Session struct {
	RoomID      string   `json:"roomId"`
	RoomCode    string   `json:"roomCode"`
	PlayerID    string   `json:"playerId"`
	PlayerToken string   `json:"playerToken"`
	LocalPiece  string   `json:"localPiece"`
	Match       Snapshot `json:"match"`
}
type Position struct {
	Row    int `json:"row"`
	Column int `json:"column"`
}
type MoveSnapshot struct {
	MoveID   string `json:"moveId"`
	PlayerID string `json:"playerId"`
	Piece    string `json:"piece"`
	Row      int    `json:"row"`
	Column   int    `json:"column"`
}
type undoSnapshot struct {
	Board          []string      `json:"board"`
	CurrentPiece   string        `json:"currentPiece"`
	MoveCount      int           `json:"moveCount"`
	Status         string        `json:"status"`
	WinnerPlayerID *string       `json:"winnerPlayerId"`
	Result         *string       `json:"result"`
	LastMove       *MoveSnapshot `json:"lastMove"`
}
type moveCheckpoint struct {
	Move   MoveSnapshot `json:"move"`
	Before undoSnapshot `json:"before"`
}
type undoHistory struct {
	Moves []moveCheckpoint `json:"moves"`
}
type MoveRequest struct {
	MoveID           string `json:"moveId"`
	ExpectedRevision int64  `json:"expectedRevision"`
	Row              int    `json:"row"`
	Column           int    `json:"column"`
}

func NewService(db *sql.DB) *Service { return &Service{db: db} }
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func hashToken(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func newID() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return hex.EncodeToString(b), nil
}
func roomCode(value int) string { return fmt.Sprintf("%04d", value) }
func isRoomCodeCollision(err error) bool {
	var sqliteErr sqlite3.Error
	return errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique
}
func (s *Service) Create(ctx context.Context, name string) (Session, error) {
	name = cleanName(name)
	id, e := newID()
	if e != nil {
		return Session{}, e
	}
	token, e := randomToken()
	if e != nil {
		return Session{}, e
	}
	seed := make([]byte, 4)
	if _, e = rand.Read(seed); e != nil {
		return Session{}, e
	}
	start := int(uint32(seed[0])<<24|uint32(seed[1])<<16|uint32(seed[2])<<8|uint32(seed[3])) % 10000
	now := time.Now().UTC().Format(time.RFC3339Nano)
	board, _ := json.Marshal(make([]string, 225))
	for attempt := 0; attempt < 10000; attempt++ {
		code := roomCode((start + attempt) % 10000)
		_, e = s.db.ExecContext(ctx, "INSERT INTO rooms(room_id,room_code,host_player_id,status,board_size,board_json,current_piece,created_at,updated_at,host_player_name,host_token_hash,host_piece,guest_piece,last_move_json,undo_snapshot_json) VALUES(?,?,?,'WAITING_FOR_OPPONENT',15,?,'BLACK',?,?,?,?,'BLACK','WHITE','null','null')", id, code, id, string(board), now, now, name, hashToken(token))
		if e == nil {
			snap, snapshotErr := s.snapshot(ctx, id)
			return Session{id, code, id, token, "BLACK", snap}, snapshotErr
		}
		if !isRoomCodeCollision(e) {
			return Session{}, e
		}
	}
	return Session{}, ErrRoomCapacity
}
func (s *Service) Join(ctx context.Context, code, name string) (Session, error) {
	var id, status string
	e := s.db.QueryRowContext(ctx, "SELECT room_id,status FROM rooms WHERE room_code=? COLLATE NOCASE AND game_type='GOMOKU'", strings.TrimSpace(code)).Scan(&id, &status)
	if e != nil {
		return Session{}, ErrNotFound
	}
	if status != "WAITING_FOR_OPPONENT" {
		return Session{}, ErrConflict
	}
	pid, e := newID()
	if e != nil {
		return Session{}, e
	}
	token, e := randomToken()
	if e != nil {
		return Session{}, e
	}
	res, e := s.db.ExecContext(ctx, "UPDATE rooms SET guest_player_id=?,guest_player_name=?,guest_token_hash=?,status='IN_PROGRESS',updated_at=? WHERE room_id=? AND status='WAITING_FOR_OPPONENT'", pid, cleanName(name), hashToken(token), time.Now().UTC().Format(time.RFC3339Nano), id)
	if e != nil {
		return Session{}, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Session{}, ErrConflict
	}
	snap, e := s.snapshot(ctx, id)
	return Session{id, snap.RoomCode, pid, token, snap.GuestPiece, snap}, e
}
func cleanName(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "玩家"
	}
	if len([]rune(v)) > 20 {
		return string([]rune(v)[:20])
	}
	return v
}
func (s *Service) snapshot(ctx context.Context, id string) (Snapshot, error) {
	var x Snapshot
	var board, lastMove, undo string
	var guestID, guestName, winner, result, pendingType, pendingPlayer sql.NullString
	e := s.db.QueryRowContext(ctx, "SELECT room_id,room_code,host_player_id,host_player_name,guest_player_id,guest_player_name,host_piece,guest_piece,status,board_size,board_json,current_piece,move_count,revision,winner_player_id,result,last_move_json,undo_snapshot_json,pending_action_type,pending_action_player_id FROM rooms WHERE room_id=?", id).Scan(&x.RoomID, &x.RoomCode, &x.HostPlayerID, &x.HostName, &guestID, &guestName, &x.HostPiece, &x.GuestPiece, &x.Status, &x.BoardSize, &board, &x.CurrentPiece, &x.MoveCount, &x.Revision, &winner, &result, &lastMove, &undo, &pendingType, &pendingPlayer)
	if e != nil {
		return x, e
	}
	_ = json.Unmarshal([]byte(board), &x.Board)
	if x.Board == nil {
		x.Board = make([]string, x.BoardSize*x.BoardSize)
	}
	if guestID.Valid {
		x.GuestPlayerID = &guestID.String
	}
	if guestName.Valid {
		x.GuestName = &guestName.String
	}
	if winner.Valid {
		x.WinnerPlayerID = &winner.String
	}
	if result.Valid {
		x.Result = &result.String
	}
	if lastMove != "" && lastMove != "null" {
		var value MoveSnapshot
		if json.Unmarshal([]byte(lastMove), &value) == nil {
			x.LastMove = &value
		}
	}
	x.undoSnapshot = undo
	if undo != "" && undo != "null" {
		var history undoHistory
		if json.Unmarshal([]byte(undo), &history) == nil {
			for _, entry := range history.Moves {
				x.RecentMovePlayers = append(x.RecentMovePlayers, entry.Move.PlayerID)
			}
		}
	}
	x.CanUndo = len(x.RecentMovePlayers) > 0
	if pendingType.Valid && pendingPlayer.Valid {
		x.PendingAction = &PendingAction{Type: pendingType.String, RequestedByPlayerID: pendingPlayer.String}
	}
	return x, nil
}
func (s *Service) AuthorizedSnapshot(ctx context.Context, id, token string) (Snapshot, string, string, error) {
	var pid, piece string
	h := hashToken(token)
	e := s.db.QueryRowContext(ctx, "SELECT CASE WHEN host_token_hash=? THEN host_player_id WHEN guest_token_hash=? THEN guest_player_id ELSE NULL END, CASE WHEN host_token_hash=? THEN host_piece WHEN guest_token_hash=? THEN guest_piece ELSE NULL END FROM rooms WHERE room_id=? AND game_type='GOMOKU'", h, h, h, h, id).Scan(&pid, &piece)
	if e != nil || pid == "" {
		return Snapshot{}, "", "", ErrNotFound
	}
	x, e := s.snapshot(ctx, id)
	return x, pid, piece, e
}
func (s *Service) Play(ctx context.Context, id, token string, m MoveRequest) (Snapshot, error) {
	snap, pid, piece, e := s.AuthorizedSnapshot(ctx, id, token)
	if e != nil {
		return Snapshot{}, e
	}
	if len(snap.Board) != snap.BoardSize*snap.BoardSize {
		return Snapshot{}, ErrSnapshotCorrupt
	}
	if m.MoveID == "" || m.Row < 0 || m.Row >= snap.BoardSize || m.Column < 0 || m.Column >= snap.BoardSize {
		return Snapshot{}, ErrInvalid
	}
	if snap.LastMove != nil && snap.LastMove.MoveID == m.MoveID {
		return snap, nil
	}
	if snap.Status != "IN_PROGRESS" || snap.PendingAction != nil || snap.Revision != m.ExpectedRevision || snap.CurrentPiece != piece {
		return Snapshot{}, ErrConflict
	}
	idx := m.Row*snap.BoardSize + m.Column
	if snap.Board[idx] != "" {
		return Snapshot{}, ErrInvalid
	}
	history := undoHistory{}
	if snap.undoSnapshot != "" && snap.undoSnapshot != "null" {
		_ = json.Unmarshal([]byte(snap.undoSnapshot), &history)
	}
	checkpoint := undoSnapshot{
		Board:          append([]string(nil), snap.Board...),
		CurrentPiece:   snap.CurrentPiece,
		MoveCount:      snap.MoveCount,
		Status:         snap.Status,
		WinnerPlayerID: snap.WinnerPlayerID,
		Result:         snap.Result,
		LastMove:       snap.LastMove,
	}
	pieceWin := false
	for _, d := range [][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}} {
		n := 1
		for _, sign := range []int{-1, 1} {
			r, c := m.Row+sign*d[0], m.Column+sign*d[1]
			for r >= 0 && r < snap.BoardSize && c >= 0 && c < snap.BoardSize && snap.Board[r*snap.BoardSize+c] == piece {
				n++
				r += sign * d[0]
				c += sign * d[1]
			}
		}
		if n >= 5 {
			pieceWin = true
		}
	}
	snap.Board[idx] = piece
	snap.LastMove = &MoveSnapshot{MoveID: m.MoveID, PlayerID: pid, Piece: piece, Row: m.Row, Column: m.Column}
	history.Moves = append(history.Moves, moveCheckpoint{Move: *snap.LastMove, Before: checkpoint})
	if len(history.Moves) > 2 {
		history.Moves = history.Moves[len(history.Moves)-2:]
	}
	rollbackBytes, _ := json.Marshal(history)
	rollback := string(rollbackBytes)
	snap.undoSnapshot = rollback
	snap.RecentMovePlayers = snap.RecentMovePlayers[:0]
	for _, entry := range history.Moves {
		snap.RecentMovePlayers = append(snap.RecentMovePlayers, entry.Move.PlayerID)
	}
	snap.MoveCount++
	snap.Revision++
	snap.CanUndo = true
	if pieceWin {
		snap.Status = "FINISHED"
		snap.WinnerPlayerID = &pid
		snap.Result = stringPtr("WIN")
	} else if snap.MoveCount == snap.BoardSize*snap.BoardSize {
		snap.Status = "FINISHED"
		snap.Result = stringPtr("DRAW")
	} else if piece == "BLACK" {
		snap.CurrentPiece = "WHITE"
	} else {
		snap.CurrentPiece = "BLACK"
	}
	board, _ := json.Marshal(snap.Board)
	last, _ := json.Marshal(snap.LastMove)
	res, e := s.db.ExecContext(ctx, "UPDATE rooms SET status=?,board_json=?,current_piece=?,move_count=?,revision=?,winner_player_id=?,result=?,last_move_json=?,undo_snapshot_json=?,updated_at=? WHERE room_id=? AND revision=? AND status='IN_PROGRESS'", snap.Status, string(board), snap.CurrentPiece, snap.MoveCount, snap.Revision, snap.WinnerPlayerID, snap.Result, string(last), rollback, time.Now().UTC().Format(time.RFC3339Nano), id, m.ExpectedRevision)
	if e != nil {
		return Snapshot{}, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Snapshot{}, ErrConflict
	}
	return snap, nil
}
func (s *Service) Restart(ctx context.Context, id, token string, expected int64) (Snapshot, error) {
	snap, _, _, e := s.AuthorizedSnapshot(ctx, id, token)
	if e != nil {
		return Snapshot{}, e
	}
	if snap.GuestPlayerID == nil || snap.PendingAction != nil || snap.Revision != expected {
		return Snapshot{}, ErrConflict
	}
	return s.reset(ctx, snap, false)
}
func (s *Service) RequestAction(ctx context.Context, id, token string, expected int64, action string) (Snapshot, error) {
	snap, playerID, _, e := s.AuthorizedSnapshot(ctx, id, token)
	if e != nil {
		return Snapshot{}, e
	}
	if snap.GuestPlayerID == nil || snap.PendingAction != nil || snap.Revision != expected {
		return Snapshot{}, ErrConflict
	}
	if action != "UNDO" && action != "SWAP_COLORS" {
		return Snapshot{}, ErrInvalid
	}
	if action == "UNDO" && !contains(snap.RecentMovePlayers, playerID) {
		return Snapshot{}, ErrConflict
	}
	res, e := s.db.ExecContext(ctx, "UPDATE rooms SET pending_action_type=?,pending_action_player_id=?,revision=revision+1,updated_at=? WHERE room_id=? AND revision=? AND pending_action_type IS NULL AND guest_player_id IS NOT NULL", action, playerID, time.Now().UTC().Format(time.RFC3339Nano), id, expected)
	if e != nil {
		return Snapshot{}, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Snapshot{}, ErrConflict
	}
	return s.snapshot(ctx, id)
}
func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
func (s *Service) RespondAction(ctx context.Context, id, token string, expected int64, accept bool) (Snapshot, error) {
	snap, responderID, _, e := s.AuthorizedSnapshot(ctx, id, token)
	if e != nil {
		return Snapshot{}, e
	}
	if snap.PendingAction == nil || snap.Revision != expected || snap.PendingAction.RequestedByPlayerID == responderID {
		return Snapshot{}, ErrConflict
	}
	if !accept {
		res, err := s.db.ExecContext(ctx, "UPDATE rooms SET pending_action_type=NULL,pending_action_player_id=NULL,revision=revision+1,updated_at=? WHERE room_id=? AND revision=? AND pending_action_player_id=?", time.Now().UTC().Format(time.RFC3339Nano), id, expected, snap.PendingAction.RequestedByPlayerID)
		if err != nil {
			return Snapshot{}, err
		}
		n, _ := res.RowsAffected()
		if n != 1 {
			return Snapshot{}, ErrConflict
		}
		return s.snapshot(ctx, id)
	}
	switch snap.PendingAction.Type {
	case "UNDO":
		return s.undoPlayer(ctx, id, snap.PendingAction.RequestedByPlayerID, expected, snap)
	case "SWAP_COLORS":
		snap.HostPiece, snap.GuestPiece = snap.GuestPiece, snap.HostPiece
		return s.reset(ctx, snap, true)
	default:
		return Snapshot{}, ErrInvalid
	}
}
func (s *Service) reset(ctx context.Context, snap Snapshot, swap bool) (Snapshot, error) {
	board, _ := json.Marshal(make([]string, snap.BoardSize*snap.BoardSize))
	now := time.Now().UTC().Format(time.RFC3339Nano)
	query := "UPDATE rooms SET status='IN_PROGRESS',board_json=?,current_piece='BLACK',move_count=0,revision=revision+1,winner_player_id=NULL,result=NULL,last_move_json='null',undo_snapshot_json='null',pending_action_type=NULL,pending_action_player_id=NULL,updated_at=? WHERE room_id=? AND revision=? AND guest_player_id IS NOT NULL"
	args := []any{string(board), now, snap.RoomID, snap.Revision}
	if swap {
		query = "UPDATE rooms SET host_piece=?,guest_piece=?,status='IN_PROGRESS',board_json=?,current_piece='BLACK',move_count=0,revision=revision+1,winner_player_id=NULL,result=NULL,last_move_json='null',undo_snapshot_json='null',pending_action_type=NULL,pending_action_player_id=NULL,updated_at=? WHERE room_id=? AND revision=? AND guest_player_id IS NOT NULL"
		args = []any{snap.HostPiece, snap.GuestPiece, string(board), now, snap.RoomID, snap.Revision}
	}
	res, e := s.db.ExecContext(ctx, query, args...)
	if e != nil {
		return Snapshot{}, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Snapshot{}, ErrConflict
	}
	return s.snapshot(ctx, snap.RoomID)
}
func (s *Service) undoPlayer(ctx context.Context, id, playerID string, expected int64, snap Snapshot) (Snapshot, error) {
	if !snap.CanUndo || snap.Revision != expected {
		return Snapshot{}, ErrConflict
	}
	var raw string
	if e := s.db.QueryRowContext(ctx, "SELECT undo_snapshot_json FROM rooms WHERE room_id=? AND revision=?", id, expected).Scan(&raw); e != nil {
		return Snapshot{}, ErrConflict
	}
	var history undoHistory
	if e := json.Unmarshal([]byte(raw), &history); e != nil {
		return Snapshot{}, e
	}
	var old *undoSnapshot
	undoIndex := -1
	for i := len(history.Moves) - 1; i >= 0; i-- {
		if history.Moves[i].Move.PlayerID == playerID {
			old = &history.Moves[i].Before
			undoIndex = i
			break
		}
	}
	if old == nil {
		return Snapshot{}, ErrConflict
	}
	if len(old.Board) != snap.BoardSize*snap.BoardSize {
		return Snapshot{}, ErrSnapshotCorrupt
	}
	// Older builds shared this slice with the live board, so the move itself
	// could leak into its pre-move snapshot. Its square had to be empty before
	// that move, making this safe for both old and new snapshots.
	restoredBoard := append([]string(nil), old.Board...)
	undoneMove := history.Moves[undoIndex].Move
	undoneIndex := undoneMove.Row*snap.BoardSize + undoneMove.Column
	if undoneIndex >= 0 && undoneIndex < len(restoredBoard) && restoredBoard[undoneIndex] == undoneMove.Piece {
		restoredBoard[undoneIndex] = ""
	}
	board, _ := json.Marshal(restoredBoard)
	last := "null"
	if old.LastMove != nil {
		b, _ := json.Marshal(old.LastMove)
		last = string(b)
	}
	remaining := undoHistory{Moves: history.Moves[:undoIndex]}
	remainingJSON := "null"
	if len(remaining.Moves) > 0 {
		b, _ := json.Marshal(remaining)
		remainingJSON = string(b)
	}
	res, e := s.db.ExecContext(ctx, "UPDATE rooms SET status=?,board_json=?,current_piece=?,move_count=?,revision=revision+1,winner_player_id=?,result=?,last_move_json=?,undo_snapshot_json=?,pending_action_type=NULL,pending_action_player_id=NULL,updated_at=? WHERE room_id=? AND revision=? AND pending_action_type='UNDO'", old.Status, string(board), old.CurrentPiece, old.MoveCount, old.WinnerPlayerID, old.Result, last, remainingJSON, time.Now().UTC().Format(time.RFC3339Nano), id, expected)
	if e != nil {
		return Snapshot{}, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Snapshot{}, ErrConflict
	}
	return s.snapshot(ctx, id)
}
func (s *Service) Resign(ctx context.Context, id, token string, expected int64) (Snapshot, error) {
	snap, playerID, _, e := s.AuthorizedSnapshot(ctx, id, token)
	if e != nil {
		return Snapshot{}, e
	}
	if snap.GuestPlayerID == nil || snap.Status != "IN_PROGRESS" || snap.Revision != expected {
		return Snapshot{}, ErrConflict
	}
	winner := snap.HostPlayerID
	if playerID == snap.HostPlayerID {
		winner = *snap.GuestPlayerID
	}
	res, e := s.db.ExecContext(ctx, "UPDATE rooms SET status='FINISHED',winner_player_id=?,result='RESIGN',revision=revision+1,undo_snapshot_json='null',pending_action_type=NULL,pending_action_player_id=NULL,updated_at=? WHERE room_id=? AND revision=? AND status='IN_PROGRESS'", winner, time.Now().UTC().Format(time.RFC3339Nano), id, expected)
	if e != nil {
		return Snapshot{}, e
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return Snapshot{}, ErrConflict
	}
	return s.snapshot(ctx, id)
}
func stringPtr(s string) *string { return &s }
func (s *Service) RoomIDForCode(ctx context.Context, code string) (string, error) {
	var id string
	e := s.db.QueryRowContext(ctx, "SELECT room_id FROM rooms WHERE room_code=? COLLATE NOCASE", code).Scan(&id)
	if e != nil {
		return "", fmt.Errorf("%w", ErrNotFound)
	}
	return id, nil
}
