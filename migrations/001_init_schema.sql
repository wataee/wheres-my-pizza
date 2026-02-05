-- 1. Таблица заказов
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

-- 2. Позиции заказа
CREATE TABLE IF NOT EXISTS order_items (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    order_id INTEGER REFERENCES orders(id),
    name TEXT NOT NULL,
    quantity INTEGER NOT NULL,
    price DECIMAL(8,2) NOT NULL
);

-- 3. Лог смены статусов (история)
CREATE TABLE IF NOT EXISTS order_status_log (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    order_id INTEGER REFERENCES orders(id),
    status TEXT,
    changed_by TEXT,
    changed_at TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    notes TEXT
);

-- 4. Воркеры (кухня)
CREATE TABLE IF NOT EXISTS workers (
    id SERIAL PRIMARY KEY,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    name TEXT UNIQUE NOT NULL,
    type TEXT NOT NULL,
    status TEXT DEFAULT 'online',
    last_seen TIMESTAMPTZ DEFAULT CURRENT_TIMESTAMP,
    orders_processed INTEGER DEFAULT 0
);

-- 5. Техническая таблица для генерации номеров заказов (Reset Daily Logic)
-- Нужна, чтобы безопасно генерировать номер 001, 002 каждый новый день.
CREATE TABLE IF NOT EXISTS daily_order_counters (
    date DATE PRIMARY KEY,
    counter INTEGER DEFAULT 0
);