package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"gomoku-server/internal/game"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/adaptor"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/gorilla/websocket"
)

type onlineAPI struct {
	s   *game.Service
	hub *game.Hub
}

func RegisterOnline(h *server.Hertz, s *game.Service, hub *game.Hub) {
	a := &onlineAPI{s, hub}
	h.POST("/api/v1/rooms", a.create)
	h.POST("/api/v1/rooms/:code/join", a.join)
	h.POST("/api/v1/rooms/:code/rejoin", a.rejoin)
	h.GET("/api/v1/matches/:id", a.snapshot)
	h.GET("/api/v1/ws", adaptor.HertzHandler(http.HandlerFunc(a.websocket)))
	h.POST("/api/v1/xiangqi/rooms", a.createXiangqi)
	h.POST("/api/v1/xiangqi/rooms/:code/join", a.joinXiangqi)
	h.POST("/api/v1/xiangqi/rooms/:code/rejoin", a.rejoinXiangqi)
}
func body(c *app.RequestContext, v any) error { return json.Unmarshal(c.Request.Body(), v) }
func (a *onlineAPI) create(ctx context.Context, c *app.RequestContext) {
	var r struct {
		DisplayName string `json:"displayName"`
	}
	if body(c, &r) != nil {
		c.JSON(400, utils.H{"error": "请求格式无效"})
		return
	}
	x, e := a.s.Create(ctx, r.DisplayName)
	if e != nil {
		fail(c, e)
		return
	}
	c.JSON(201, x)
}
func (a *onlineAPI) join(ctx context.Context, c *app.RequestContext) {
	var r struct {
		DisplayName string `json:"displayName"`
	}
	if body(c, &r) != nil {
		c.JSON(400, utils.H{"error": "请求格式无效"})
		return
	}
	x, e := a.s.Join(ctx, string(c.Param("code")), r.DisplayName)
	if e != nil {
		fail(c, e)
		return
	}
	a.hub.Broadcast(x.RoomID, map[string]any{"type": "state", "match": x.Match})
	c.JSON(200, x)
}
func (a *onlineAPI) createXiangqi(ctx context.Context, c *app.RequestContext) {
	var r struct {
		DisplayName string `json:"displayName"`
	}
	if body(c, &r) != nil {
		c.JSON(400, utils.H{"error": "请求格式无效"})
		return
	}
	x, e := a.s.CreateXiangqi(ctx, r.DisplayName)
	if e != nil {
		fail(c, e)
		return
	}
	c.JSON(201, x)
}
func (a *onlineAPI) joinXiangqi(ctx context.Context, c *app.RequestContext) {
	var r struct {
		DisplayName string `json:"displayName"`
	}
	if body(c, &r) != nil {
		c.JSON(400, utils.H{"error": "请求格式无效"})
		return
	}
	x, e := a.s.JoinXiangqi(ctx, string(c.Param("code")), r.DisplayName)
	if e != nil {
		fail(c, e)
		return
	}
	a.hub.Broadcast(x.RoomID, map[string]any{"type": "state", "match": x.Match})
	c.JSON(200, x)
}
func (a *onlineAPI) rejoin(ctx context.Context, c *app.RequestContext) {
	var r struct {
		PlayerToken string `json:"playerToken"`
	}
	if body(c, &r) != nil || r.PlayerToken == "" {
		c.JSON(400, utils.H{"error": "请求格式无效"})
		return
	}
	x, e := a.s.Rejoin(ctx, string(c.Param("code")), r.PlayerToken)
	if e != nil {
		fail(c, e)
		return
	}
	c.JSON(200, x)
}
func (a *onlineAPI) rejoinXiangqi(ctx context.Context, c *app.RequestContext) {
	var r struct {
		PlayerToken string `json:"playerToken"`
	}
	if body(c, &r) != nil || r.PlayerToken == "" {
		c.JSON(400, utils.H{"error": "请求格式无效"})
		return
	}
	x, e := a.s.RejoinXiangqi(ctx, string(c.Param("code")), r.PlayerToken)
	if e != nil {
		fail(c, e)
		return
	}
	c.JSON(200, x)
}
func auth(c *app.RequestContext) string {
	v := string(c.Request.Header.Peek("Authorization"))
	return strings.TrimPrefix(v, "Bearer ")
}
func (a *onlineAPI) snapshot(ctx context.Context, c *app.RequestContext) {
	x, _, _, e := a.s.AuthorizedSnapshot(ctx, string(c.Param("id")), auth(c))
	if e != nil {
		fail(c, e)
		return
	}
	c.JSON(200, x)
}
func fail(c *app.RequestContext, e error) {
	switch {
	case errors.Is(e, game.ErrNotFound):
		c.JSON(404, utils.H{"error": e.Error()})
	case errors.Is(e, game.ErrConflict):
		c.JSON(409, utils.H{"error": e.Error()})
	case errors.Is(e, game.ErrInvalid):
		c.JSON(422, utils.H{"error": e.Error()})
	case errors.Is(e, game.ErrRoomCapacity):
		c.JSON(503, utils.H{"error": e.Error()})
	case errors.Is(e, game.ErrSnapshotCorrupt):
		c.JSON(409, utils.H{"error": e.Error()})
	default:
		c.JSON(500, utils.H{"error": "服务端错误"})
	}
}
func (a *onlineAPI) websocket(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("roomId")
	token := r.URL.Query().Get("token")
	if snap, pid, e := a.s.AuthorizedXiangqi(r.Context(), id, token); e == nil {
		a.xiangqiWebsocket(w, r, id, token, pid, snap)
		return
	}
	snap, pid, _, e := a.s.AuthorizedSnapshot(r.Context(), id, token)
	if e != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	conn, e := up.Upgrade(w, r, nil)
	if e != nil {
		return
	}
	client := &game.Client{Conn: conn, PlayerID: pid}
	a.hub.Add(id, client)
	a.hub.Broadcast(id, map[string]any{"type": "presence", "playerId": pid, "connected": true})
	defer func() {
		stillConnected := a.hub.Remove(id, client)
		conn.Close()
		if !stillConnected {
			a.hub.Broadcast(id, map[string]any{"type": "presence", "playerId": pid, "connected": false})
		}
	}()
	_ = client.WriteJSON(map[string]any{"type": "state", "match": snap})
	for {
		var msg struct {
			Type             string `json:"type"`
			MoveID           string `json:"moveId"`
			ExpectedRevision int64  `json:"expectedRevision"`
			Row              int    `json:"row"`
			Column           int    `json:"column"`
		}
		if conn.ReadJSON(&msg) != nil {
			return
		}
		var next game.Snapshot
		switch msg.Type {
		case "move":
			next, e = a.s.Play(r.Context(), id, token, game.MoveRequest{MoveID: msg.MoveID, ExpectedRevision: msg.ExpectedRevision, Row: msg.Row, Column: msg.Column})
		case "restart":
			next, e = a.s.Restart(r.Context(), id, token, msg.ExpectedRevision)
		case "request_undo":
			next, e = a.s.RequestAction(r.Context(), id, token, msg.ExpectedRevision, "UNDO")
		case "request_swap_colors":
			next, e = a.s.RequestAction(r.Context(), id, token, msg.ExpectedRevision, "SWAP_COLORS")
		case "accept_action":
			next, e = a.s.RespondAction(r.Context(), id, token, msg.ExpectedRevision, true)
		case "reject_action":
			next, e = a.s.RespondAction(r.Context(), id, token, msg.ExpectedRevision, false)
		case "resign":
			next, e = a.s.Resign(r.Context(), id, token, msg.ExpectedRevision)
		default:
			continue
		}
		if e != nil {
			_ = client.WriteJSON(map[string]any{"type": "error", "message": e.Error()})
			continue
		}
		a.hub.Broadcast(id, map[string]any{"type": "state", "match": next})
	}
}

func (a *onlineAPI) xiangqiWebsocket(w http.ResponseWriter, r *http.Request, id, token, pid string, snap game.XiangqiSnapshot) {
	up := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	conn, e := up.Upgrade(w, r, nil)
	if e != nil {
		return
	}
	client := &game.Client{Conn: conn, PlayerID: pid}
	a.hub.Add(id, client)
	a.hub.Broadcast(id, map[string]any{"type": "presence", "playerId": pid, "connected": true})
	defer func() {
		stillConnected := a.hub.Remove(id, client)
		conn.Close()
		if !stillConnected {
			a.hub.Broadcast(id, map[string]any{"type": "presence", "playerId": pid, "connected": false})
		}
	}()
	_ = client.WriteJSON(map[string]any{"type": "state", "match": snap})
	for {
		var msg struct {
			Type             string `json:"type"`
			ExpectedRevision int64  `json:"expectedRevision"`
			FromRow          int    `json:"fromRow"`
			FromColumn       int    `json:"fromColumn"`
			ToRow            int    `json:"toRow"`
			ToColumn         int    `json:"toColumn"`
		}
		if conn.ReadJSON(&msg) != nil {
			return
		}
		log.Printf("xiangqi message room=%s player=%s type=%s from=(%d,%d) to=(%d,%d) revision=%d", id, pid, msg.Type, msg.FromRow, msg.FromColumn, msg.ToRow, msg.ToColumn, msg.ExpectedRevision)
		var next game.XiangqiSnapshot
		if msg.Type == "move" {
			next, e = a.s.PlayXiangqi(r.Context(), id, token, msg.ExpectedRevision, game.XiangqiMove{FromRow: msg.FromRow, FromColumn: msg.FromColumn, ToRow: msg.ToRow, ToColumn: msg.ToColumn})
		} else {
			next, e = a.s.XiangqiAction(r.Context(), id, token, msg.Type, msg.ExpectedRevision)
		}
		if e != nil {
			log.Printf("xiangqi rejected room=%s player=%s type=%s error=%v", id, pid, msg.Type, e)
			_ = client.WriteJSON(map[string]any{"type": "error", "message": e.Error()})
			continue
		}
		log.Printf("xiangqi updated room=%s revision=%d moves=%d status=%s turn=%s", id, next.Revision, next.MoveCount, next.Status, next.CurrentTurn)
		a.hub.Broadcast(id, map[string]any{"type": "state", "match": next})
	}
}
