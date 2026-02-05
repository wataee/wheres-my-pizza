package tracking

import (
	"encoding/json"
	"net/http"
	"restaurant-system/internal/constants"
	"restaurant-system/internal/logger"
	"restaurant-system/internal/models"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Handler struct {
	Service *Service
	DB      *pgxpool.Pool
}

func NewHandler(s *Service, db *pgxpool.Pool) *Handler {
	return &Handler{Service: s, DB: db}
}

func (h *Handler) GetOrderStatus(w http.ResponseWriter, r *http.Request) {
	orderNum := strings.TrimPrefix(r.URL.Path, "/orders/")
	orderNum = strings.TrimSuffix(orderNum, "/status")

	logger.LogDebug("request_received", "Get order status request", "",
		"endpoint", "/orders/{order_number}/status",
		"order_number", orderNum)

	resp, err := h.Service.GetOrderStatus(r.Context(), orderNum)
	if err != nil {
		logger.LogError("db_query_failed", "Failed to query order status", err, "")
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error"})
		return
	}
	if resp == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "Order not found"})
		return
	}

	respondJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetOrderHistory(w http.ResponseWriter, r *http.Request) {
	orderNum := strings.TrimPrefix(r.URL.Path, "/orders/")
	orderNum = strings.TrimSuffix(orderNum, "/history")

	logger.LogDebug("request_received", "Get order history request", "",
		"endpoint", "/orders/{order_number}/history",
		"order_number", orderNum)

	resp, err := h.Service.GetOrderHistory(r.Context(), orderNum)
	if err != nil {
		logger.LogError("db_query_failed", "Failed to query order history", err, "")
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error"})
		return
	}

	respondJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetWorkersStatus(w http.ResponseWriter, r *http.Request) {
	logger.LogDebug("request_received", "Get workers status request", "",
		"endpoint", "/workers/status")

	resp, err := h.Service.GetWorkersStatus(r.Context())
	if err != nil {
		logger.LogError("db_query_failed", "Failed to query workers status", err, "")
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "Database error"})
		return
	}

	respondJSON(w, http.StatusOK, resp)
}

func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	
	checks := make(map[string]string)
	allHealthy := true

	if err := h.DB.Ping(ctx); err != nil {
		checks["database"] = "unhealthy: " + err.Error()
		allHealthy = false
	} else {
		checks["database"] = "healthy"
	}

	status := "healthy"
	httpStatus := http.StatusOK
	if !allHealthy {
		status = "unhealthy"
		httpStatus = http.StatusServiceUnavailable
	}

	response := models.HealthCheckResponse{
		Status:    status,
		Timestamp: time.Now().UTC(),
		Service:   constants.ServiceTrackingService,
		Checks:    checks,
	}

	respondJSON(w, httpStatus, response)
}

func respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
