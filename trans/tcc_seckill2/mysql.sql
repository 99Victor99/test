-- 事务主表
CREATE TABLE tcc_transaction (
  tx_id VARCHAR(64) PRIMARY KEY COMMENT '全局事务ID',
  status ENUM('TRYING','TRIED','CONFIRMING','CONFIRMED','CANCELLING','CANCELLED','FAILED') NOT NULL, #FAILED：全局重试超限或超时，触发人工干预或补偿。
  retry_count TINYINT UNSIGNED DEFAULT 0 COMMENT '全局重试次数', # 记录 Confirm/Cancel 阶段的整体重试次数,
  last_retry_time DATETIME DEFAULT NULL COMMENT '上次重试时间' # 控制重试间隔（如指数增长：1s, 2s, 4s）,
  error_message VARCHAR(255) DEFAULT NULL COMMENT '最后失败原因' # 记录全局失败原因（如 "branch inventory timeout"）,
  max_retry TINYINT UNSIGNED DEFAULT 5 COMMENT '最大重试次数', # 限制全局重试，超过转为 FAILED.
  timeout_time DATETIME DEFAULT NULL COMMENT '事务超时时间', # 设置全局超时（如 30 分钟），超时转为 FAILED.
  create_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  update_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_update_time (update_time)
) ENGINE=InnoDB ROW_FORMAT=COMPRESSED;

-- 分支事务表
CREATE TABLE tcc_branch (
  branch_id BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
  tx_id VARCHAR(64) NOT NULL,
  resource_type VARCHAR(32) COMMENT '资源类型',
  resource_id VARCHAR(128) COMMENT '资源标识',
  status ENUM('PREPARED','COMMITTED','ROLLBACKED','FAILED') NOT NULL,
  snapshot JSON COMMENT '资源快照',
  compensation_data JSON COMMENT '补偿数据',
  retry_count TINYINT UNSIGNED DEFAULT 0 COMMENT '重试次数',
  last_retry_time DATETIME DEFAULT NULL COMMENT '上次重试时间', # 分支级时间控制，与全局类似
  max_retry TINYINT UNSIGNED DEFAULT 5 COMMENT '分支最大重试次数', # 分支级限制，允许不同资源自定义（如库存重试 3 次，订单 10 次）
  error_message VARCHAR(255) DEFAULT NULL COMMENT '最后失败原因', # 记录分支失败细节（如 "inventory insufficient"）
  create_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  FOREIGN KEY(tx_id) REFERENCES tcc_transaction(tx_id),
  KEY idx_tx_resource (tx_id, resource_type)
) ENGINE=InnoDB;

-- tcc_error_log 设计成能记录 全局级 和 分支级，区分方式是：
-- branch_id IS NULL → 全局事务的重试失败
-- branch_id NOT NULL → 分支事务的重试失败

CREATE TABLE tcc_error_log (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tx_id VARCHAR(64) NOT NULL,
  branch_id BIGINT UNSIGNED DEFAULT NULL COMMENT '为空表示全局事务错误',
  phase ENUM('TRY','CONFIRM','CANCEL') NOT NULL,
  retry_count TINYINT UNSIGNED NOT NULL,
  error_message VARCHAR(255) NOT NULL,
  create_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  FOREIGN KEY(tx_id) REFERENCES tcc_transaction(tx_id),
  FOREIGN KEY(branch_id) REFERENCES tcc_branch(branch_id)
);


-- 业务表
CREATE TABLE seckill_inventory (
  item_id BIGINT PRIMARY KEY COMMENT '商品ID',
  total INT UNSIGNED NOT NULL COMMENT '总库存',
  frozen INT UNSIGNED DEFAULT 0 COMMENT '冻结库存',
  version INT UNSIGNED DEFAULT 0 COMMENT '版本号',
  KEY idx_version (version)
) ENGINE=InnoDB;

CREATE TABLE user_account (
  user_id BIGINT PRIMARY KEY,
  balance DECIMAL(18,4) UNSIGNED NOT NULL,
  frozen_balance DECIMAL(18,4) UNSIGNED DEFAULT 0,
  version INT UNSIGNED DEFAULT 0
) ENGINE=InnoDB;

CREATE TABLE seckill_orders (
  order_id VARCHAR(64) PRIMARY KEY,
  user_id BIGINT NOT NULL,
  item_id BIGINT NOT NULL,
  quantity INT UNSIGNED NOT NULL,
  amount DECIMAL(18,4) UNSIGNED NOT NULL,
  status ENUM('PRE_CREATE','CREATED','CANCELED') NOT NULL,
  create_time DATETIME DEFAULT CURRENT_TIMESTAMP
) ENGINE=InnoDB;