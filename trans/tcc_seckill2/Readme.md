-- 中央事务主表 db_log
CREATE TABLE tcc_transaction (
  tx_id VARCHAR(64) PRIMARY KEY COMMENT '全局事务ID',
  status ENUM('TRYING','TRIED','CONFIRMING','CONFIRMED','CANCELLING','CANCELLED','FAILED') NOT NULL, #FAILED：全局重试超限或超时，触发人工干预或补偿。
  participant_count TINYINT DEFAULT 0,  -- 预期参与方数量
  retry_count TINYINT UNSIGNED DEFAULT 0 COMMENT '全局重试次数', # 记录 Confirm/Cancel 阶段的整体重试次数,
  last_retry_time DATETIME DEFAULT NULL COMMENT '上次重试时间' # 控制重试间隔（如指数增长：1s, 2s, 4s）,
  error_message VARCHAR(255) DEFAULT NULL COMMENT '最后失败原因' # 记录全局失败原因（如 "branch inventory timeout"）,
  max_retry TINYINT UNSIGNED DEFAULT 5 COMMENT '最大重试次数', # 限制全局重试，超过转为 FAILED.
  timeout_time DATETIME DEFAULT NULL COMMENT '事务超时时间', # 设置全局超时（如 30 分钟），超时转为 FAILED.
  create_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  update_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  KEY idx_update_time (update_time)
) ENGINE=InnoDB ROW_FORMAT=COMPRESSED;


-- 中央重试日志记录, 记录协调过程中的错误
CREATE TABLE tcc_error_log (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tx_id VARCHAR(64) NOT NULL,
  branch_id BIGINT DEFAULT 0 COMMENT '分支ID，0表示全局错误';
  resource_type VARCHAR(32) DEFAULT NULL COMMENT '资源类型';
  phase ENUM('TRY','CONFIRM','CANCEL') NOT NULL,
  retry_count TINYINT UNSIGNED DEFAULT 0 COMMENT 'phase阶段的第几次重试',
  error_message VARCHAR(255) NOT NULL,
  create_time DATETIME(3) DEFAULT CURRENT_TIMESTAMP(3),
  KEY idx_tx_id (tx_id),
  KEY idx_create_time (create_time)
);


-- tcc_error_log 设计成能记录 全局级 和 分支级，区分方式是：
-- branch_id IS NULL → 全局事务的重试失败
-- branch_id NOT NULL → 分支事务的重试失败

-- 本地分支事务表: RM自己负责持久化,与业务同一个事务周期, 防止重试重复扣减
CREATE TABLE local_tcc_branch (
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


SQL 流程

---- Try ----

-- db_log （初始化）：
	INSERT INTO db_log.tcc_transaction (tx_id, status, participant_count, max_retry, timeout_time, create_time) VALUES ('tx100', 'TRYING', 3, 3, 30, NOW());

-- 协调器任务分发
-- db1 （库存）：
    # 本地事务幂等; 仅会存在 PREPARED(已准备), 未插入: no_row
    SELECT status FROM local_tcc_branch WHERE tx_id = 'tx100' AND resource_type = 'inventory';
    # IF status = 'PREPARED' THEN RETURN;

	BEGIN;
		UPDATE seckill_inventory SET frozen = frozen + ?, available = available - ? WHERE item_id = ?;

		# 插入事务分支(PREPARED状态)
		INSERT INTO local_tcc_branch (branch_id, tx_id, resource_type, status, create_time) VALUES (?, 'tx100', 'inventory', 'PREPARED', NOW());
	COMMIT;

-- db2 （账户）：
	SELECT status FROM local_tcc_branch WHERE tx_id = 'tx100' AND resource_type = 'account'
    # IF

	BEGIN;
		UPDATE user_account SET frozen = frozen + ?, balance = balance - ? WHERE user_id = ?;

		INSERT INTO local_tcc_branch (branch_id, tx_id, resource_type, status, create_time) VALUES (?, 'tx100', 'account', 'PREPARED', NOW());
	COMMIT;

-- db3 （订单）： # 纯插入无补偿量的直接基于业务表, 可不用本地事务日志表, 业务操作简单; 将事务号和状态插入实体表.
    SELECT status FROM seckill_orders WHERE tx_id = 'tx100' AND resource_type = 'order';
    # IF

	BEGIN;
		INSERT INTO db3.seckill_orders (order_id, status, tx_id, resource_type) VALUES (?, 'PREPARED', 'tx100', 'order');
	COMMIT;

-- db_log ：

	-- 错误处理: 分发任务存在io err, 则协调器try重试 --
	# 事务主表记录重试次数++
	UPDATE db_log.tcc_transaction SET retry_count = retry_count+1, last_retry_time = NOW(), error_message=err, update_time = NOW() WHERE tx_id = 'tx100';
	
	# 重试日志记录 retry_count: try阶段第几次错误
	INSERT INTO db_log.tcc_error_log (tx_id, phase, retry_count, error_message, create_time) VALUES ('tx100', 'TRY', 1, err, NOW());

	# 其他错误回滚

	-- 事务主表校验更新 --
	SELECT COUNT(1) FROM db1.local_tcc_branch WHERE tx_id = 'tx100' AND status = 'PREPARED';
	SELECT COUNT(1) FROM db2.local_tcc_branch WHERE tx_id = 'tx100' AND status = 'PREPARED';
	SELECT COUNT(1) FROM db3.seckill_orders WHERE tx_id = 'tx100' AND status = 'PREPARED';
	-- 若计数<预期参与方数量，触发重试

	UPDATE db_log.tcc_transaction SET status = 'TRIED', update_time = NOW() WHERE tx_id = 'tx100';


---- Confirm阶段 ----

-- 先将状态更新为CONFIRMING
UPDATE db_log.tcc_transaction SET status = 'CONFIRMING' WHERE tx_id = ? AND status = 'TRIED';

-- 协调器任务分发
	-- db1 库存
    # 本地事务幂等; 仅会存在 PREPARED(已准备) CONFIRMED(已提交)
    SELECT status FROM local_tcc_branch WHERE tx_id=? and resource_type='inventory';
    IF status = 'CONFIRMED' THEN RETURN;

    IF status = 'PREPARED' THEN
        BEGIN;
                UPDATE seckill_inventory SET frozen = frozen - ?, total = total - ? WHERE item_id = ?;
            
                # 业务分支-- 状态流转（安全, 保证只允许 PREPARED → CONFIRMED）
                UPDATE local_tcc_branch SET status = 'CONFIRMED' WHERE tx_id = ? and resource_type='inventory' and status='PREPARED';
        COMMIT;
    END IF;

	-- db2 账户
    SELECT status FROM local_tcc_branch WHERE tx_id=? and resource_type='account';
    # IF

	BEGIN;
		UPDATE user_account SET frozen = frozen - ? WHERE user_id = ?;

		UPDATE local_tcc_branch SET status = 'CONFIRMED' WHERE tx_id = ? and resource_type='account' and status='PREPARED';
	COMMIT;

	-- db3 订单: 
	BEGIN;
		SELECT status FROM seckill_orders WHERE tx_id=? and order_id=?;
        # IF

		UPDATE seckill_orders SET status = 'CONFIRMED' WHERE order_id = ?;
	COMMIT;

-- db_log ：

	-- 错误处理: 分发任务存在io err, 则协调器confirm重试 --
	# 事务主表记录重试次数++
	UPDATE db_log.tcc_transaction SET retry_count = retry_count+1, last_retry_time = NOW(), error_message=err update_time = NOW() WHERE tx_id = 'tx100';
	
	# 重试日志记录 retry_count: confirm阶段第几次错误
	INSERT INTO db_log.tcc_error_log (tx_id, phase, retry_count, error_message, create_time) VALUES ('tx100', 'CONFIRM', 1, err, NOW());

	# 其他错误回滚

	-- 事务主表校验更新 --
	SELECT COUNT(1) FROM db1.local_tcc_branch WHERE tx_id = 'tx100' AND status = 'CONFIRMED';
	SELECT COUNT(1) FROM db2.local_tcc_branch WHERE tx_id = 'tx100' AND status = 'CONFIRMED';
	SELECT COUNT(1) FROM db3.seckill_orders WHERE tx_id = 'tx100' AND status = 'CONFIRMED';
	-- 若计数<预期参与方数量，触发重试

	-- 最终更新全局事务状态为CONFIRMED
	UPDATE db_log.tcc_transaction SET status = 'CONFIRMED' WHERE tx_id = ?;


---- Cancel阶段 ----

-- 先将中间态状态更新为CANCELLING
UPDATE db_log.tcc_transaction SET status = 'CANCELLING' WHERE tx_id = ? AND status IN ('TRYING','TRIED','CONFIRMING');

	-- 执行业务取消操作
	-- db1 库存
    # 本地事务幂等; 仅会存在 PREPARED(已准备) CONFIRMED(已提交) CANCELLED(已取消)
    SELECT status FROM local_tcc_branch WHERE tx_id=? and resource_type='inventory';
    IF status = 'CANCELLED' THEN RETURN; -- 幂等

	# 资源归还
    IF status IN ('PREPARED', 'CONFIRMED') THEN
        BEGIN;
            UPDATE seckill_inventory SET frozen = frozen - ?, available = available + ? WHERE item_id = ?;

            # 业务分支更新状态(CONFIRMED状态)
            UPDATE local_tcc_branch SET status = 'CANCELLED' WHERE tx_id = ? and resource_type='inventory' and status IN ('PREPARED', 'CONFIRMED');
        COMMIT;
    END IF;

	-- db2 账户

    SELECT status FROM local_tcc_branch WHERE tx_id=? and resource_type='account';
    # IF

	# 资源归还
    IF status IN ('PREPARED', 'CONFIRMED') THEN
        BEGIN;
            UPDATE db2.user_account SET frozen = frozen - ?, balance = balance + ? WHERE user_id = ?;

            UPDATE db_log.local_tcc_branch SET status = 'CANCELLED' WHERE tx_id = ? and resource_type='account' and status IN ('PREPARED', 'CONFIRMED');
        COMMIT;
    # IF

	-- db3 订单
    SELECT status FROM db3.seckill_orders WHERE tx_id = 'tx100';
	# IF
    IF status IN ('PREPARED', 'CONFIRMED') THEN
        UPDATE seckill_orders SET status = 'CANCELLED' WHERE tx_id = ? and status IN ('PREPARED', 'CONFIRMED'); 

-- db_log ：

	-- 错误处理: 分发任务存在io err, 则协调器cannel重试 --
	# 事务主表记录重试次数++
	UPDATE db_log.tcc_transaction SET retry_count = retry_count+1, last_retry_time = NOW(), error_message=err, update_time = NOW() WHERE tx_id = 'tx100';
	
	# 重试日志记录 retry_count: CANCEL阶段第几次错误
	INSERT INTO db_log.tcc_error_log (tx_id, phase, retry_count, error_message, create_time) VALUES ('tx100', 'CANCEL', 1, err, NOW());

	# 其他错误回滚

	-- 事务主表校验更新 --
	SELECT COUNT(1) FROM db1.local_tcc_branch WHERE tx_id = 'tx100' AND status = 'CANCELLED';
	SELECT COUNT(1) FROM db2.local_tcc_branch WHERE tx_id = 'tx100' AND status = 'CANCELLED';
	SELECT COUNT(1) FROM db3.seckill_orders WHERE tx_id = 'tx100' AND status = 'CANCELLED';
	-- 若计数<预期参与方数量，触发重试

-- 最终更新全局事务状态为CANCELLED
UPDATE db_log.tcc_transaction SET status = 'CANCELLED' WHERE tx_id = ?;



-- 定时任务处理中断事务, 定义所有事务尽量完成 --
-- 搜索中间态事务
-- 中央协调器查询需要补偿的全局事务
SELECT * FROM tcc_transaction 
WHERE status IN ('TRYING','TRIED','CONFIRMING','CANCELLING')
  AND retry_count < max_retry
  AND NOW() < timeout_time
ORDER BY update_time
LIMIT 100;

# TRYING:事务准备中,分支记录补偿量可能不全, 实体属性可能变动, 一般回滚
case 'TRYING': # 本地事务包含PREPARED状态
	调用协调器的Cannel()

# TRIED:事务已完成准备阶段, 分支记录补偿量完整.
case 'TRIED': # 本地事务包含PREPARED状态
	1 查询补偿量, 组织传参. 不在意状态
	SELECT * FROM local_tcc_branch WHERE tx_id = ?;
	
	2 调用协调器的Confirm(); 自带幂等回滚.

# CONFIRMING: 事务提交中
case 'CONFIRMING': # 本地事务包含PREPARED CONFIRMED状态
	1 查询补偿量, 组织传参. 不在意状态
	SELECT * FROM local_tcc_branch WHERE tx_id = ?;
	
	2 调用协调器的Confirm(); 自带幂等回滚.
	
# CANCELLING: 事务回滚中
case 'CANCELLING': # 本地事务包含PREPARED CONFIRMED CANCELLED状态
	1 查询补偿量, 组织传参. 不在意状态
	SELECT * FROM local_tcc_branch WHERE tx_id = ?;
	
	2 调用协调器的Cannel(); 自带幂等.


# 超时即失败：
UPDATE tcc_transaction
   SET status='FAILED', error_message='transaction timeout'
 WHERE tx_id=? AND NOW() > timeout_time;


