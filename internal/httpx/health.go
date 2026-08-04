package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type healthResponse struct {
	Status string `json:"status"`
	DB     string `json:"db"`
	Time   string `json:"time"`
}

// health reports liveness plus the database's reachability as a field rather
// than as a failure, so the binary is diagnosable when Postgres is down.
func health(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, healthResponse{
			Status: "ok",
			DB:     dbStatus(r.Context(), pool),
			Time:   time.Now().UTC().Format(time.RFC3339),
		})
	}
}

func dbStatus(ctx context.Context, pool *pgxpool.Pool) string {
	if pool == nil {
		return "not_configured"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := pool.Ping(ctx); err != nil {
		return "down"
	}
	return "up"
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
