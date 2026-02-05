package tracking

import (
	"context"
	"restaurant-system/internal/models"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB *pgxpool.Pool
}

func NewService(db *pgxpool.Pool) *Service {
	return &Service{DB: db}
}

func (s *Service) GetOrderStatus(ctx context.Context, orderNum string) (*models.OrderStatusResponse, error) {
	var r models.OrderStatusResponse
	var completedAt *time.Time
	var processedBy *string

	err := s.DB.QueryRow(ctx, `
		SELECT number, status, updated_at, completed_at, processed_by
		FROM orders WHERE number = $1
	`, orderNum).Scan(&r.OrderNumber, &r.CurrentStatus, &r.UpdatedAt, &completedAt, &processedBy)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}

	if processedBy != nil {
		r.ProcessedBy = *processedBy
	}

	if r.CurrentStatus == "cooking" {
		r.EstimatedCompletion = r.UpdatedAt.Add(10 * time.Second)
	} else if completedAt != nil {
		r.EstimatedCompletion = *completedAt
	}

	return &r, nil
}

func (s *Service) GetOrderHistory(ctx context.Context, orderNum string) ([]models.OrderHistoryEntry, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT l.status, l.changed_at, l.changed_by
		FROM order_status_log l
		JOIN orders o ON o.id = l.order_id
		WHERE o.number = $1
		ORDER BY l.changed_at ASC
	`, orderNum)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var history []models.OrderHistoryEntry
	for rows.Next() {
		var h models.OrderHistoryEntry
		if err := rows.Scan(&h.Status, &h.Timestamp, &h.ChangedBy); err != nil {
			return nil, err
		}
		history = append(history, h)
	}

	return history, nil
}

func (s *Service) GetWorkersStatus(ctx context.Context) ([]models.WorkerStatus, error) {
	rows, err := s.DB.Query(ctx, `
		SELECT name, status, orders_processed, last_seen
		FROM workers
		ORDER BY name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workers []models.WorkerStatus
	for rows.Next() {
		var w models.WorkerStatus
		if err := rows.Scan(&w.WorkerName, &w.Status, &w.OrdersProcessed, &w.LastSeen); err != nil {
			return nil, err
		}

		if time.Since(w.LastSeen) > 60*time.Second {
			w.Status = "offline"
		}
		workers = append(workers, w)
	}

	return workers, nil
}