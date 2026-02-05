-- Orders table
CREATE TABLE IF NOT EXISTS orders (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    number TEXT UNIQUE NOT NULL,
    customer_name TEXT NOT NULL,
    type TEXT NOT NULL CHECK (type IN ('dine_in', 'takeout', 'delivery')),
    table_number INTEGER,
    delivery_address TEXT,
    total_amount DECIMAL(10,2) NOT NULL,
    priority INTEGER DEFAULT 1,
    status TEXT DEFAULT 'received',
    processed_by TEXT,
    completed_at TIMESTAMPTZ
);

CREATE INDEX idx_orders_number ON orders(number);
CREATE INDEX idx_orders_status ON orders(status);
CREATE INDEX idx_orders_created_at ON orders(created_at);

-- Order items table
CREATE TABLE IF NOT EXISTS order_items (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    order_id INTEGER REFERENCES orders(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    quantity INTEGER NOT NULL,
    price DECIMAL(8,2) NOT NULL
);

CREATE INDEX idx_order_items_order_id ON order_items(order_id);

-- Order status log table
CREATE TABLE IF NOT EXISTS order_status_log (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    order_id INTEGER REFERENCES orders(id) ON DELETE CASCADE,
    status TEXT,
    changed_by TEXT,
    changed_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    notes TEXT
);

CREATE INDEX idx_order_status_log_order_id ON order_status_log(order_id);
CREATE INDEX idx_order_status_log_changed_at ON order_status_log(changed_at);

-- Workers table
CREATE TABLE IF NOT EXISTS workers (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    name TEXT UNIQUE NOT NULL,
    type TEXT NOT NULL,
    status TEXT DEFAULT 'online',
    last_seen TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    orders_processed INTEGER DEFAULT 0
);

CREATE INDEX idx_workers_name ON workers(name);
CREATE INDEX idx_workers_status ON workers(status);

-- Daily order counters table
CREATE TABLE IF NOT EXISTS daily_order_counters (
    date DATE PRIMARY KEY,
    counter INTEGER DEFAULT 0
);

CREATE INDEX idx_daily_order_counters_date ON daily_order_counters(date);
