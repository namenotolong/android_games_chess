package httpapi

import (
	"context"
	"database/sql"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

func HealthHandler(db *sql.DB) func(context.Context, *app.RequestContext) {
	return func(ctx context.Context, c *app.RequestContext) {
		dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()

		if err := db.PingContext(dbCtx); err != nil {
			c.JSON(consts.StatusServiceUnavailable, utils.H{
				"status":   "unhealthy",
				"database": "unavailable",
			})
			return
		}
		c.JSON(consts.StatusOK, utils.H{
			"status":   "ok",
			"database": "ok",
		})
	}
}
