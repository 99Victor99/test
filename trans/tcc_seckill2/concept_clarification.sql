-- TCC事务表设计概念澄清

-- =================================
-- 方案1：中央协调模式（类似Seata TC）
-- =================================

-- 协调器数据库 (TC)
CREATE TABLE global_transaction (
    xid VARCHAR(128) PRIMARY KEY,
    status TINYINT NOT NULL,
    timeout INT,
    begin_time BIGINT,
    application_data VARCHAR(2000)
);

-- 中央分支事务表（在协调器数据库中）
CREATE TABLE central_branch_table (
    branch_id BIGINT PRIMARY KEY,
    xid VARCHAR(128),
    resource_group_id VARCHAR(32),
    resource_id VARCHAR(256),           -- 抽象的资源标识
    branch_type VARCHAR(8),
    status TINYINT,                     -- 抽象的状态
    client_id VARCHAR(64),
    application_data VARCHAR(2000)      -- 可能包含部分补偿信息
);

-- 业务服务数据库 (RM)
CREATE TABLE account (
    id BIGINT PRIMARY KEY,
    user_id VARCHAR(100),
    money DECIMAL(10,2)
);

-- 业务相关的补偿表（在业务数据库中）
CREATE TABLE account_freeze (
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    user_id VARCHAR(100),
    freeze_money DECIMAL(10,2),         -- 具体的补偿数据
    state VARCHAR(20),                  -- 本地状态管理
    xid VARCHAR(128)                    -- 关联全局事务
);

-- =================================
-- 方案2：本地分支模式（我之前的设计）
-- =================================

-- 简化的协调器表（或者没有独立协调器）
CREATE TABLE tcc_transaction (
    tx_id VARCHAR(64) PRIMARY KEY,
    status ENUM('TRYING','TRIED','CONFIRMING','CONFIRMED','CANCELLING','CANCELLED'),
    create_time DATETIME(3)
);

-- 业务服务数据库中的本地分支事务表
-- 这就是我之前说的"冻结表"，实际上是本地分支事务表
CREATE TABLE inventory_local_branch (  -- 之前误称为 inventory_freeze
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tx_id VARCHAR(64) NOT NULL,
    item_id BIGINT NOT NULL,            -- 资源标识
    freeze_quantity INT UNSIGNED,       -- 具体补偿数据
    state ENUM('TRIED','CONFIRMED','CANCELLED'), -- 本地分支状态
    create_time DATETIME(3),
    UNIQUE KEY uk_tx_item (tx_id, item_id)
);

CREATE TABLE account_local_branch (    -- 之前误称为 account_freeze  
    id BIGINT AUTO_INCREMENT PRIMARY KEY,
    tx_id VARCHAR(64) NOT NULL,
    user_id BIGINT NOT NULL,            -- 资源标识
    freeze_amount DECIMAL(18,4),        -- 具体补偿数据
    state ENUM('TRIED','CONFIRMED','CANCELLED'), -- 本地分支状态
    create_time DATETIME(3),
    UNIQUE KEY uk_tx_user (tx_id, user_id)
);

-- =================================
-- 关键区别总结：
-- =================================

-- 中央分支表特点：
-- 1. 在协调器数据库中，统一管理所有分支
-- 2. 记录抽象的资源ID和状态  
-- 3. 具体的补偿数据在各业务库的补偿表中
-- 4. TC可以统一查询所有分支状态

-- 本地分支表特点：  
-- 1. 在各业务数据库中，分散管理
-- 2. 状态+补偿数据一体化存储
-- 3. 无需额外的补偿表
-- 4. 协调器需要逐个调用各服务查询状态

-- =================================
-- 我之前的概念混淆：
-- =================================
-- 错误认知：认为"冻结表"是一种新的设计模式
-- 正确理解：所谓"冻结表"就是"本地分支事务表"
-- 真正区别：在于分支表是中央管理还是本地管理

-- Seata的真正创新：
-- 不是发明了"冻结表"概念
-- 而是巧妙地将分支管理和补偿数据分离：
-- - TC端：抽象的分支状态管理  
-- - RM端：具体的业务补偿数据

-- 我们项目的设计：
-- 采用本地分支表模式（分散管理）
-- 将状态管理和补偿数据合并在一张表中
-- 这是完全合理和有效的设计选择