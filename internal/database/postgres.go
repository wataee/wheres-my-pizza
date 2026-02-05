package database

import (
	"context"
	"fmt"
	"restaurant-system/internal/logger"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	// Connection pool settings
	MaxConns          = 25
	MinConns          = 5
	MaxConnLifetime   = 1 * time.Hour
	MaxConnIdleTime   = 30 * time.Minute
	HealthCheckPeriod = 1 * time.Minute

	// Retry settings
	MaxRetries     = 3
	InitialBackoff = 1 * time.Second
	MaxBackoff     = 30 * time.Second
)

// Client represents a database client with health monitoring
type Client struct {
	pool    *pgxpool.Pool
	connStr string
	mu      sync.RWMutex
	healthy bool
}

// Connect establishes a connection to PostgreSQL with retry logic
func Connect(ctx context.Context, connString string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	// Configure connection pool
	config.MaxConns = MaxConns
	config.MinConns = MinConns
	config.MaxConnLifetime = MaxConnLifetime
	config.MaxConnIdleTime = MaxConnIdleTime
	config.HealthCheckPeriod = HealthCheckPeriod

	// Connection timeout
	config.ConnConfig.ConnectTimeout = 10 * time.Second

	var pool *pgxpool.Pool
	backoff := InitialBackoff

	// Retry connection with exponential backoff
	for attempt := 1; attempt <= MaxRetries; attempt++ {
		pool, err = pgxpool.NewWithConfig(ctx, config)
		if err == nil {
			// Test connection
			if err = pool.Ping(ctx); err == nil {
				logger.LogInfo("db_connected", "Connected to PostgreSQL database", "startup",
					"max_conns", MaxConns,
					"min_conns", MinConns,
					"attempt", attempt)
				return pool, nil
			}
			pool.Close()
		}

		if attempt < MaxRetries {
			logger.LogError("db_connection_retry", 
				fmt.Sprintf("Failed to connect to database (attempt %d/%d), retrying in %v", 
					attempt, MaxRetries, backoff), err, "startup")
			
			time.Sleep(backoff)
			backoff *= 2
			if backoff > MaxBackoff {
				backoff = MaxBackoff
			}
		}
	}

	return nil, fmt.Errorf("failed to connect to database after %d attempts: %w", MaxRetries, err)
}

// NewClient creates a new database client with health monitoring
func NewClient(ctx context.Context, connString string) (*Client, error) {
	pool, err := Connect(ctx, connString)
	if err != nil {
		return nil, err
	}

	client := &Client{
		pool:    pool,
		connStr: connString,
		healthy: true,
	}

	// Start health monitoring
	go client.monitorHealth(ctx)

	return client, nil
}

// GetPool returns the connection pool
func (c *Client) GetPool() *pgxpool.Pool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pool
}

// IsHealthy returns the current health status
func (c *Client) IsHealthy() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.healthy
}

// Close closes the database connection
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if c.pool != nil {
		c.pool.Close()
		logger.LogInfo("db_closed", "Database connection closed", "")
	}
}

// monitorHealth periodically checks database health
func (c *Client) monitorHealth(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.checkHealth(ctx)
		}
	}
}

// checkHealth performs a health check
func (c *Client) checkHealth(ctx context.Context) {
	c.mu.RLock()
	pool := c.pool
	c.mu.RUnlock()

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err := pool.Ping(pingCtx)
	
	c.mu.Lock()
	wasHealthy := c.healthy
	c.healthy = (err == nil)
	c.mu.Unlock()

	if !c.healthy && wasHealthy {
		logger.LogError("db_health_check_failed", "Database health check failed", err, "health")
	} else if c.healthy && !wasHealthy {
		logger.LogInfo("db_health_restored", "Database health restored", "health")
	}
}

// RunInTx executes a function within a database transaction
func RunInTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	// Add timeout to context if not already present
	_, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			// Panic occurred, rollback
			_ = tx.Rollback(ctx)
			panic(p)
		} else if err != nil {
			// Error occurred, rollback
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				logger.LogError("tx_rollback_failed", "Failed to rollback transaction", rbErr, "")
			}
		} else {
			// Success, commit
			err = tx.Commit(ctx)
			if err != nil {
				logger.LogError("tx_commit_failed", "Failed to commit transaction", err, "")
			}
		}
	}()

	err = fn(tx)
	return err
}

// RunInTxWithRetry executes a function within a transaction with retry logic
func RunInTxWithRetry(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) error {
	backoff := 100 * time.Millisecond
	maxRetries := 3

	for attempt := 1; attempt <= maxRetries; attempt++ {
		err := RunInTx(ctx, pool, fn)
		if err == nil {
			return nil
		}

		// Check if error is retryable (serialization failure, deadlock, etc.)
		if !isRetryableError(err) {
			return err
		}

		if attempt < maxRetries {
			logger.LogError("tx_retry", 
				fmt.Sprintf("Transaction failed (attempt %d/%d), retrying", attempt, maxRetries),
				err, "")
			
			time.Sleep(backoff)
			backoff *= 2
		} else {
			return fmt.Errorf("transaction failed after %d attempts: %w", maxRetries, err)
		}
	}

	return nil
}


func isRetryableError(err error) bool {
    var pgErr *pgconn.PgError
    
    // Используем errors.As для безопасного извлечения ошибки PostgreSQL
    if !errors.As(err, &pgErr) {
        return false
    }

    // Коды ошибок PostgreSQL
    switch pgErr.Code {
    case "40001": // serialization_failure
        return true
    case "40P01": // deadlock_detected
        return true
    default:
        return false
    }
}