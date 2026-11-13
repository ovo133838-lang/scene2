-- 题目02 宠物门店预约系统 数据库 Schema（SQLite，WAL）
-- 金额一律人民币分(int64)；时间存 UTC RFC3339 文本
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL CHECK(role IN ('manager','user','reception','staff','service')),
  display_name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS pets (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  owner_id INTEGER NOT NULL REFERENCES users(id),
  name TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS time_slots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  start_at TEXT NOT NULL,
  end_at TEXT NOT NULL,
  capacity INTEGER NOT NULL DEFAULT 3,
  status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open','closed'))
);

CREATE TABLE IF NOT EXISTS bookings (
  id TEXT PRIMARY KEY,
  slot_id INTEGER NOT NULL REFERENCES time_slots(id),
  user_id INTEGER NOT NULL REFERENCES users(id),
  pet_id INTEGER NOT NULL REFERENCES pets(id),
  status TEXT NOT NULL CHECK(status IN (
    'pending_payment','confirmed','cancel_review','cancelled',
    'checked_in','in_service','completed','no_show','closed','payment_anomaly')),
  cancel_reason TEXT,
  review_decision TEXT CHECK(review_decision IS NULL OR review_decision IN ('approved','rejected')),
  review_note TEXT,
  no_show_reason TEXT,
  created_at TEXT NOT NULL,
  paid_at TEXT,
  pay_deadline TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_bookings_slot_pet ON bookings(slot_id, pet_id);
CREATE INDEX IF NOT EXISTS idx_bookings_status ON bookings(status);

CREATE TABLE IF NOT EXISTS slot_taken (
  booking_id TEXT PRIMARY KEY REFERENCES bookings(id),
  slot_id INTEGER NOT NULL REFERENCES time_slots(id)
);
CREATE INDEX IF NOT EXISTS idx_slot_taken_slot ON slot_taken(slot_id);

CREATE TABLE IF NOT EXISTS payments (
  id TEXT PRIMARY KEY,
  booking_id TEXT NOT NULL REFERENCES bookings(id),
  amount INTEGER NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('initiated','paid','failed')),
  channel_txn TEXT UNIQUE,
  created_at TEXT NOT NULL,
  paid_at TEXT
);

CREATE TABLE IF NOT EXISTS payment_notifications (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  channel_txn TEXT,
  booking_id TEXT,
  amount INTEGER,
  sig_ok INTEGER NOT NULL,
  result TEXT NOT NULL,
  handled TEXT NOT NULL,
  received_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS refunds (
  id TEXT PRIMARY KEY,
  booking_id TEXT NOT NULL REFERENCES bookings(id),
  payment_id TEXT NOT NULL REFERENCES payments(id),
  amount INTEGER NOT NULL,
  status TEXT NOT NULL CHECK(status IN ('pending','success','failed')),
  fail_reason TEXT,
  attempt INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL,
  done_at TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_refunds_payment ON refunds(payment_id);

CREATE TABLE IF NOT EXISTS service_records (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  booking_id TEXT NOT NULL REFERENCES bookings(id),
  action TEXT NOT NULL CHECK(action IN ('checkin','start','finish')),
  operator_id INTEGER NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  UNIQUE(booking_id, action)
);

CREATE TABLE IF NOT EXISTS audit_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  actor_id INTEGER,
  action TEXT NOT NULL,
  detail TEXT,
  created_at TEXT NOT NULL
);
