-- 改进版本：借鉴Seata冻结表设计思路
-- db_log 事务协调表（简化版TC）
CREATE TABLE tcc_transaction (
  tx_id VARCHAR(64) PRIMARY KEY COMMENT '全局事务ID',
  status ENUM('TRYING','TRIED','CONFIRMING','CONFIRMED','CANCELLING','CANCELLED','FAILED') NOT NULL,
  timeout_time DATETIME DEFAULT NULL COMMENT '事务超时时间',
  create_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  update_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3)
) ENGINE=InnoDB;
-- db_log 分支事务表
CREATE TABLE tcc_branch (
    branch_id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    tx_id VARCHAR(64) NOT NULL COMMENT '全局事务ID',
    resource_type VARCHAR(32) NOT NULL COMMENT '资源类型，如: inventory, order',
    action_name VARCHAR(128) NOT NULL COMMENT 'TCC动作名称',
    status ENUM('PREPARED', 'CONFIRMED', 'CANCELLED') NOT NULL DEFAULT 'PREPARED' COMMENT '分支状态',
    snapshot JSON NOT NULL COMMENT '业务快照(用于Cancel)', -- 例如: {"frozen_amount": 10, "item_id": 100}
    create_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
    update_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
    UNIQUE KEY uk_tx_id_resource (tx_id, resource_type) -- 同一个全局事务中，同一资源只应有一条记录
    KEY idx_tx_status (tx_id, status),
    KEY idx_status_update_time (status, update_time)
) ENGINE=InnoDB COMMENT='本地TCC分支事务表';

-- 业务表：库存
CREATE TABLE seckill_inventory (
  item_id BIGINT PRIMARY KEY,
  total INT UNSIGNED NOT NULL COMMENT '总库存',
  available INT UNSIGNED NOT NULL COMMENT '可用库存'
) ENGINE=InnoDB;

-- 冻结表：库存冻结记录（借鉴Seata设计）幂等, 补偿量
CREATE TABLE inventory_freeze (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tx_id VARCHAR(64) NOT NULL,
  item_id BIGINT NOT NULL,
  freeze_quantity INT UNSIGNED NOT NULL COMMENT '冻结数量',
  state ENUM('TRIED','CONFIRMED','CANCELLED') NOT NULL DEFAULT 'TRIED',
  create_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  update_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_tx_item (tx_id, item_id)
) ENGINE=InnoDB;

-- 业务表：用户账户
CREATE TABLE user_account (
  user_id BIGINT PRIMARY KEY,
  balance DECIMAL(18,4) UNSIGNED NOT NULL,
  available_balance DECIMAL(18,4) UNSIGNED NOT NULL COMMENT '可用余额'
) ENGINE=InnoDB;

-- 冻结表：账户冻结记录
CREATE TABLE account_freeze (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tx_id VARCHAR(64) NOT NULL,
  user_id BIGINT NOT NULL,
  freeze_amount DECIMAL(18,4) UNSIGNED NOT NULL,
  state ENUM('TRIED','CONFIRMED','CANCELLED') NOT NULL DEFAULT 'TRIED',
  create_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  update_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_tx_user (tx_id, user_id)
) ENGINE=InnoDB;

-- 业务表：订单
CREATE TABLE seckill_orders (
  order_id VARCHAR(64) PRIMARY KEY,
  tx_id VARCHAR(64) NOT NULL,
  user_id BIGINT NOT NULL,
  item_id BIGINT NOT NULL,
  quantity INT UNSIGNED NOT NULL,
  amount DECIMAL(18,4) UNSIGNED NOT NULL,
  status ENUM('CREATED','CANCELLED') NOT NULL DEFAULT 'CREATED',
  create_time DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB;