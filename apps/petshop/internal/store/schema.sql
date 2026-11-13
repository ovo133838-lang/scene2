-- 题目02 宠物门店预约系统 数据库 Schema（MySQL 8，utf8mb4）
-- 金额一律人民币分(BIGINT)；时间存 DATETIME(UTC)
-- 连接：root/root@tcp(127.0.0.1:3306)/petshop_t02（库名由服务启动时自动创建）

CREATE TABLE IF NOT EXISTS users (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  username VARCHAR(64) UNIQUE NOT NULL,
  password_hash VARCHAR(128) NOT NULL,
  role VARCHAR(16) NOT NULL,
  display_name VARCHAR(64) NOT NULL,
  created_at DATETIME NOT NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS pets (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  owner_id BIGINT NOT NULL,
  name VARCHAR(64) NOT NULL,
  CONSTRAINT fk_pets_owner FOREIGN KEY (owner_id) REFERENCES users(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS time_slots (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  start_at DATETIME NOT NULL,
  end_at DATETIME NOT NULL,
  capacity INT NOT NULL DEFAULT 3,
  status VARCHAR(16) NOT NULL DEFAULT 'open',
  created_at DATETIME NOT NULL,
  KEY idx_slots_start (start_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS bookings (
  id VARCHAR(32) PRIMARY KEY,
  slot_id BIGINT NOT NULL,
  user_id BIGINT NOT NULL,
  pet_id BIGINT NOT NULL,
  status VARCHAR(20) NOT NULL,
  cancel_reason VARCHAR(255) NULL,
  review_decision VARCHAR(16) NULL,
  review_note VARCHAR(255) NULL,
  no_show_reason VARCHAR(255) NULL,
  active TINYINT NULL,          -- 1=有效占用态；终态置 NULL（NULL 不参与唯一约束，允许同宠物同时段历史多次）
  created_at DATETIME NOT NULL,
  paid_at DATETIME NULL,
  pay_deadline DATETIME NOT NULL,
  UNIQUE KEY uk_slot_pet_active (slot_id, pet_id, active),
  KEY idx_bookings_status (status),
  CONSTRAINT fk_bookings_slot FOREIGN KEY (slot_id) REFERENCES time_slots(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS slot_taken (
  booking_id VARCHAR(32) PRIMARY KEY,
  slot_id BIGINT NOT NULL,
  KEY idx_slot_taken_slot (slot_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS payments (
  id VARCHAR(32) PRIMARY KEY,
  booking_id VARCHAR(32) NOT NULL,
  amount BIGINT NOT NULL,
  status VARCHAR(16) NOT NULL,
  channel_txn VARCHAR(64) UNIQUE,
  created_at DATETIME NOT NULL,
  paid_at DATETIME NULL,
  KEY idx_payments_booking (booking_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS payment_notifications (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  channel_txn VARCHAR(64) NULL,
  booking_id VARCHAR(32) NULL,
  amount BIGINT NULL,
  sig_ok TINYINT NOT NULL,
  result VARCHAR(32) NOT NULL,
  handled VARCHAR(64) NOT NULL,
  received_at DATETIME NOT NULL,
  KEY idx_notif_booking (booking_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS refunds (
  id VARCHAR(32) PRIMARY KEY,
  booking_id VARCHAR(32) NOT NULL,
  payment_id VARCHAR(32) NOT NULL,
  amount BIGINT NOT NULL,
  status VARCHAR(16) NOT NULL,
  fail_reason VARCHAR(255) NULL,
  attempt INT NOT NULL DEFAULT 1,
  created_at DATETIME NOT NULL,
  done_at DATETIME NULL,
  UNIQUE KEY uk_refunds_payment (payment_id),
  KEY idx_refunds_booking (booking_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS service_records (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  booking_id VARCHAR(32) NOT NULL,
  action VARCHAR(16) NOT NULL,
  operator_id BIGINT NOT NULL,
  created_at DATETIME NOT NULL,
  UNIQUE KEY uk_service_booking_action (booking_id, action)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS complaints (
  id VARCHAR(32) PRIMARY KEY,
  booking_id VARCHAR(32) NOT NULL,
  user_id BIGINT NOT NULL,
  reason VARCHAR(255) NOT NULL,
  status VARCHAR(24) NOT NULL,
  decision_note VARCHAR(255) NULL,
  created_at DATETIME NOT NULL,
  handled_at DATETIME NULL,
  UNIQUE KEY uk_complaints_booking (booking_id),
  KEY idx_complaints_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS audit_logs (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  actor_id BIGINT NULL,
  action VARCHAR(64) NOT NULL,
  detail VARCHAR(512) NULL,
  created_at DATETIME NOT NULL,
  KEY idx_audit_action (action)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
