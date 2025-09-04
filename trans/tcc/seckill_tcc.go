package main

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// SeckillTCCContext 秒杀TCC上下文
type SeckillTCCContext struct {
	TransactionID string        // 事务ID
	UserID        int64         // 用户ID
	ProductID     int64         // 商品ID
	Quantity      int           // 购买数量
	Price         float64       // 商品价格
	CreatedAt     time.Time     // 创建时间
	Timeout       time.Duration // 超时时间
}

// SeckillTCCResource TCC资源接口
type SeckillTCCResource interface {
	Try(ctx *SeckillTCCContext) error
	Confirm(ctx *SeckillTCCContext) error
	Cancel(ctx *SeckillTCCContext) error
}

// SeckillInventoryResource 秒杀库存资源（重点优化）
type SeckillInventoryResource struct {
	db    *sql.DB
	mutex sync.RWMutex // 读写锁保护
}

func NewSeckillInventoryResource(db *sql.DB) *SeckillInventoryResource {
	return &SeckillInventoryResource{db: db}
}

// Try 预扣库存 - 高并发优化版本
func (sir *SeckillInventoryResource) Try(ctx *SeckillTCCContext) error {
	tx, err := sir.db.Begin()
	if err != nil {
		return fmt.Errorf("开始事务失败: %v", err)
	}
	defer tx.Rollback()

	// 1. 使用行锁查询当前库存（FOR UPDATE确保并发安全）
	var currentStock int
	err = tx.QueryRow(`
		SELECT stock FROM seckill_inventory 
		WHERE product_id = ? FOR UPDATE
	`, ctx.ProductID).Scan(&currentStock)
	if err != nil {
		return fmt.Errorf("查询商品库存失败: %v", err)
	}

	// 2. 检查库存是否充足
	if currentStock < ctx.Quantity {
		return fmt.Errorf("库存不足: 剩余%d, 需要%d", currentStock, ctx.Quantity)
	}

	// 3. 原子性扣减可用库存，增加冻结库存
	result, err := tx.Exec(`
		UPDATE seckill_inventory 
		SET stock = stock - ?, frozen_stock = frozen_stock + ?, updated_at = ? 
		WHERE product_id = ? AND stock >= ?
	`, ctx.Quantity, ctx.Quantity, time.Now(), ctx.ProductID, ctx.Quantity)
	if err != nil {
		return fmt.Errorf("冻结库存失败: %v", err)
	}

	// 4. 检查是否真正更新了记录（防止并发导致的库存不足）
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("检查更新结果失败: %v", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("库存不足或商品不存在")
	}

	// 5. 记录冻结详情（用于后续Confirm/Cancel）
	_, err = tx.Exec(`
		INSERT INTO seckill_inventory_freeze 
		(transaction_id, product_id, quantity, status, created_at, expires_at) 
		VALUES (?, ?, ?, 'FROZEN', ?, ?)
	`, ctx.TransactionID, ctx.ProductID, ctx.Quantity, ctx.CreatedAt, ctx.CreatedAt.Add(ctx.Timeout))
	if err != nil {
		return fmt.Errorf("记录冻结信息失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %v", err)
	}

	log.Printf("[Seckill Try] 成功冻结商品%d库存%d个", ctx.ProductID, ctx.Quantity)
	return nil
}

// Confirm 确认扣库存 - 将冻结库存转为已售
func (sir *SeckillInventoryResource) Confirm(ctx *SeckillTCCContext) error {
	tx, err := sir.db.Begin()
	if err != nil {
		return fmt.Errorf("开始事务失败: %v", err)
	}
	defer tx.Rollback()

	// 1. 检查冻结记录是否存在
	var frozenQuantity int
	err = tx.QueryRow(`
		SELECT quantity FROM seckill_inventory_freeze 
		WHERE transaction_id = ? AND product_id = ? AND status = 'FROZEN'
	`, ctx.TransactionID, ctx.ProductID).Scan(&frozenQuantity)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("未找到冻结记录")
		}
		return fmt.Errorf("查询冻结记录失败: %v", err)
	}

	// 2. 减少冻结库存，增加已售库存
	_, err = tx.Exec(`
		UPDATE seckill_inventory 
		SET frozen_stock = frozen_stock - ?, sold_stock = sold_stock + ?, updated_at = ? 
		WHERE product_id = ?
	`, frozenQuantity, frozenQuantity, time.Now(), ctx.ProductID)
	if err != nil {
		return fmt.Errorf("确认库存扣减失败: %v", err)
	}

	// 3. 更新冻结记录状态
	_, err = tx.Exec(`
		UPDATE seckill_inventory_freeze 
		SET status = 'CONFIRMED', updated_at = ? 
		WHERE transaction_id = ? AND product_id = ?
	`, time.Now(), ctx.TransactionID, ctx.ProductID)
	if err != nil {
		return fmt.Errorf("更新冻结记录失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %v", err)
	}

	log.Printf("[Seckill Confirm] 成功确认商品%d库存%d个", ctx.ProductID, frozenQuantity)
	return nil
}

// Cancel 取消扣库存 - 释放冻结库存
func (sir *SeckillInventoryResource) Cancel(ctx *SeckillTCCContext) error {
	tx, err := sir.db.Begin()
	if err != nil {
		return fmt.Errorf("开始事务失败: %v", err)
	}
	defer tx.Rollback()

	// 1. 查询冻结记录
	var frozenQuantity int
	var status string
	err = tx.QueryRow(`
		SELECT quantity, status FROM seckill_inventory_freeze 
		WHERE transaction_id = ? AND product_id = ? AND status IN ('FROZEN', 'CONFIRMED')
	`, ctx.TransactionID, ctx.ProductID).Scan(&frozenQuantity, &status)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("[Seckill Cancel] 未找到需要取消的记录")
			return nil
		}
		return fmt.Errorf("查询冻结记录失败: %v", err)
	}

	// 2. 根据状态进行不同的处理
	if status == "FROZEN" {
		// 冻结状态：释放冻结库存回到可用库存
		_, err = tx.Exec(`
			UPDATE seckill_inventory 
			SET stock = stock + ?, frozen_stock = frozen_stock - ?, updated_at = ? 
			WHERE product_id = ?
		`, frozenQuantity, frozenQuantity, time.Now(), ctx.ProductID)
	} else if status == "CONFIRMED" {
		// 已确认状态：从已售库存中恢复到可用库存
		_, err = tx.Exec(`
			UPDATE seckill_inventory 
			SET stock = stock + ?, sold_stock = sold_stock - ?, updated_at = ? 
			WHERE product_id = ?
		`, frozenQuantity, frozenQuantity, time.Now(), ctx.ProductID)
	}
	if err != nil {
		return fmt.Errorf("释放库存失败: %v", err)
	}

	// 3. 更新冻结记录状态
	_, err = tx.Exec(`
		UPDATE seckill_inventory_freeze 
		SET status = 'CANCELLED', updated_at = ? 
		WHERE transaction_id = ? AND product_id = ?
	`, time.Now(), ctx.TransactionID, ctx.ProductID)
	if err != nil {
		return fmt.Errorf("更新冻结记录失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %v", err)
	}

	log.Printf("[Seckill Cancel] 成功取消商品%d库存%d个，状态:%s", ctx.ProductID, frozenQuantity, status)
	return nil
}

// SeckillAccountResource 秒杀账户资源
type SeckillAccountResource struct {
	db *sql.DB
}

func NewSeckillAccountResource(db *sql.DB) *SeckillAccountResource {
	return &SeckillAccountResource{db: db}
}

// Try 预扣余额
func (sar *SeckillAccountResource) Try(ctx *SeckillTCCContext) error {
	tx, err := sar.db.Begin()
	if err != nil {
		return fmt.Errorf("开始事务失败: %v", err)
	}
	defer tx.Rollback()

	totalAmount := ctx.Price * float64(ctx.Quantity)

	// 1. 检查余额是否充足（行锁）
	var balance float64
	err = tx.QueryRow(`
		SELECT balance FROM seckill_account 
		WHERE user_id = ? FOR UPDATE
	`, ctx.UserID).Scan(&balance)
	if err != nil {
		return fmt.Errorf("查询账户余额失败: %v", err)
	}

	if balance < totalAmount {
		return fmt.Errorf("余额不足: 余额%.2f, 需要%.2f", balance, totalAmount)
	}

	// 2. 冻结金额
	_, err = tx.Exec(`
		UPDATE seckill_account 
		SET balance = balance - ?, frozen_balance = frozen_balance + ?, updated_at = ? 
		WHERE user_id = ? AND balance >= ?
	`, totalAmount, totalAmount, time.Now(), ctx.UserID, totalAmount)
	if err != nil {
		return fmt.Errorf("冻结余额失败: %v", err)
	}

	// 3. 记录冻结详情
	_, err = tx.Exec(`
		INSERT INTO seckill_account_freeze 
		(transaction_id, user_id, amount, status, created_at, expires_at) 
		VALUES (?, ?, ?, 'FROZEN', ?, ?)
	`, ctx.TransactionID, ctx.UserID, totalAmount, ctx.CreatedAt, ctx.CreatedAt.Add(ctx.Timeout))
	if err != nil {
		return fmt.Errorf("记录冻结信息失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %v", err)
	}

	log.Printf("[Seckill Account Try] 成功冻结用户%d余额%.2f", ctx.UserID, totalAmount)
	return nil
}

// Confirm 确认扣款
func (sar *SeckillAccountResource) Confirm(ctx *SeckillTCCContext) error {
	tx, err := sar.db.Begin()
	if err != nil {
		return fmt.Errorf("开始事务失败: %v", err)
	}
	defer tx.Rollback()

	// 1. 查询冻结金额
	var frozenAmount float64
	err = tx.QueryRow(`
		SELECT amount FROM seckill_account_freeze 
		WHERE transaction_id = ? AND user_id = ? AND status = 'FROZEN'
	`, ctx.TransactionID, ctx.UserID).Scan(&frozenAmount)
	if err != nil {
		return fmt.Errorf("查询冻结记录失败: %v", err)
	}

	// 2. 确认扣款：减少冻结余额
	_, err = tx.Exec(`
		UPDATE seckill_account 
		SET frozen_balance = frozen_balance - ?, updated_at = ? 
		WHERE user_id = ?
	`, frozenAmount, time.Now(), ctx.UserID)
	if err != nil {
		return fmt.Errorf("确认扣款失败: %v", err)
	}

	// 3. 更新冻结记录状态
	_, err = tx.Exec(`
		UPDATE seckill_account_freeze 
		SET status = 'CONFIRMED', updated_at = ? 
		WHERE transaction_id = ? AND user_id = ?
	`, time.Now(), ctx.TransactionID, ctx.UserID)
	if err != nil {
		return fmt.Errorf("更新冻结记录失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %v", err)
	}

	log.Printf("[Seckill Account Confirm] 成功确认用户%d扣款%.2f", ctx.UserID, frozenAmount)
	return nil
}

// Cancel 取消扣款
func (sar *SeckillAccountResource) Cancel(ctx *SeckillTCCContext) error {
	tx, err := sar.db.Begin()
	if err != nil {
		return fmt.Errorf("开始事务失败: %v", err)
	}
	defer tx.Rollback()

	// 1. 查询冻结记录
	var frozenAmount float64
	var status string
	err = tx.QueryRow(`
		SELECT amount, status FROM seckill_account_freeze 
		WHERE transaction_id = ? AND user_id = ? AND status IN ('FROZEN', 'CONFIRMED')
	`, ctx.TransactionID, ctx.UserID).Scan(&frozenAmount, &status)
	if err != nil {
		if err == sql.ErrNoRows {
			log.Printf("[Seckill Account Cancel] 未找到需要取消的记录")
			return nil
		}
		return fmt.Errorf("查询冻结记录失败: %v", err)
	}

	// 2. 根据状态进行不同处理
	if status == "FROZEN" {
		// 冻结状态：释放冻结余额回到可用余额
		_, err = tx.Exec(`
			UPDATE seckill_account 
			SET balance = balance + ?, frozen_balance = frozen_balance - ?, updated_at = ? 
			WHERE user_id = ?
		`, frozenAmount, frozenAmount, time.Now(), ctx.UserID)
	} else if status == "CONFIRMED" {
		// 已确认状态：退款到可用余额
		_, err = tx.Exec(`
			UPDATE seckill_account 
			SET balance = balance + ?, updated_at = ? 
			WHERE user_id = ?
		`, frozenAmount, time.Now(), ctx.UserID)
	}
	if err != nil {
		return fmt.Errorf("释放余额失败: %v", err)
	}

	// 3. 更新冻结记录状态
	_, err = tx.Exec(`
		UPDATE seckill_account_freeze 
		SET status = 'CANCELLED', updated_at = ? 
		WHERE transaction_id = ? AND user_id = ?
	`, time.Now(), ctx.TransactionID, ctx.UserID)
	if err != nil {
		return fmt.Errorf("更新冻结记录失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %v", err)
	}

	log.Printf("[Seckill Account Cancel] 成功取消用户%d金额%.2f，状态:%s", ctx.UserID, frozenAmount, status)
	return nil
}

// SeckillOrderResource 秒杀订单资源
type SeckillOrderResource struct {
	db *sql.DB
}

func NewSeckillOrderResource(db *sql.DB) *SeckillOrderResource {
	return &SeckillOrderResource{db: db}
}

// Try 创建预订单
func (sor *SeckillOrderResource) Try(ctx *SeckillTCCContext) error {
	totalAmount := ctx.Price * float64(ctx.Quantity)

	_, err := sor.db.Exec(`
		INSERT INTO seckill_orders 
		(transaction_id, user_id, product_id, quantity, price, total_amount, status, created_at) 
		VALUES (?, ?, ?, ?, ?, ?, 'PENDING', ?)
	`, ctx.TransactionID, ctx.UserID, ctx.ProductID, ctx.Quantity, ctx.Price, totalAmount, ctx.CreatedAt)
	if err != nil {
		return fmt.Errorf("创建预订单失败: %v", err)
	}

	log.Printf("[Seckill Order Try] 成功创建预订单，用户%d商品%d", ctx.UserID, ctx.ProductID)
	return nil
}

// Confirm 确认订单
func (sor *SeckillOrderResource) Confirm(ctx *SeckillTCCContext) error {
	_, err := sor.db.Exec(`
		UPDATE seckill_orders 
		SET status = 'CONFIRMED', updated_at = ? 
		WHERE transaction_id = ? AND user_id = ?
	`, time.Now(), ctx.TransactionID, ctx.UserID)
	if err != nil {
		return fmt.Errorf("确认订单失败: %v", err)
	}

	log.Printf("[Seckill Order Confirm] 成功确认订单，用户%d", ctx.UserID)
	return nil
}

// Cancel 取消订单
func (sor *SeckillOrderResource) Cancel(ctx *SeckillTCCContext) error {
	_, err := sor.db.Exec(`
		UPDATE seckill_orders 
		SET status = 'CANCELLED', updated_at = ? 
		WHERE transaction_id = ? AND user_id = ?
	`, time.Now(), ctx.TransactionID, ctx.UserID)
	if err != nil {
		return fmt.Errorf("取消订单失败: %v", err)
	}

	log.Printf("[Seckill Order Cancel] 成功取消订单，用户%d", ctx.UserID)
	return nil
}

// SeckillTCCManager 秒杀TCC管理器
type SeckillTCCManager struct {
	resources []SeckillTCCResource
	mu        sync.RWMutex
}

func NewSeckillTCCManager() *SeckillTCCManager {
	return &SeckillTCCManager{
		resources: make([]SeckillTCCResource, 0),
	}
}

// AddResource 添加TCC资源
func (stm *SeckillTCCManager) AddResource(resource SeckillTCCResource) {
	stm.mu.Lock()
	defer stm.mu.Unlock()
	stm.resources = append(stm.resources, resource)
}

// ExecuteSeckillTCC 执行秒杀TCC事务
func (stm *SeckillTCCManager) ExecuteSeckillTCC(ctx *SeckillTCCContext) error {
	stm.mu.RLock()
	defer stm.mu.RUnlock()

	log.Printf("[Seckill TCC] 开始执行秒杀事务: %s", ctx.TransactionID)

	// Phase 1: Try阶段 - 预留所有资源
	// var trySuccessCount int
	for i, resource := range stm.resources {
		if err := resource.Try(ctx); err != nil {
			log.Printf("[Seckill TCC] Try阶段失败，资源%d: %v", i, err)
			// Try失败，回滚已成功的Try操作
			stm.cancelResources(ctx)
			return fmt.Errorf("秒杀TCC Try阶段失败: %v", err)
		}
		// trySuccessCount++
	}

	log.Printf("[Seckill TCC] Try阶段成功完成，开始Confirm阶段")

	// Phase 2: Confirm阶段 - 确认提交
	for i, resource := range stm.resources {
		if err := resource.Confirm(ctx); err != nil {
			log.Printf("[Seckill TCC] Confirm阶段失败，资源%d: %v", i, err)
			// Confirm失败，执行Cancel补偿
			stm.cancelResources(ctx)
			return fmt.Errorf("秒杀TCC Confirm阶段失败: %v", err)
		}
	}

	log.Printf("[Seckill TCC] 秒杀事务成功完成: %s", ctx.TransactionID)
	return nil
}

// cancelResources 取消资源（补偿操作）
func (stm *SeckillTCCManager) cancelResources(ctx *SeckillTCCContext) {
	log.Printf("[Seckill TCC] 开始执行Cancel补偿操作")
	for i, resource := range stm.resources {
		if err := resource.Cancel(ctx); err != nil {
			log.Printf("[Seckill TCC] Cancel补偿失败，资源%d: %v", i, err)
		}
	}
}

// 初始化秒杀数据库表结构
func initSeckillDatabase(db *sql.DB) error {
	tables := []string{
		// 秒杀库存表（优化版）
		`CREATE TABLE IF NOT EXISTS seckill_inventory (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			product_id BIGINT NOT NULL UNIQUE,
			stock INT NOT NULL DEFAULT 0 COMMENT '可用库存',
			frozen_stock INT NOT NULL DEFAULT 0 COMMENT '冻结库存',
			sold_stock INT NOT NULL DEFAULT 0 COMMENT '已售库存',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_product_id (product_id)
		)`,
		// 库存冻结记录表
		`CREATE TABLE IF NOT EXISTS seckill_inventory_freeze (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			transaction_id VARCHAR(64) NOT NULL,
			product_id BIGINT NOT NULL,
			quantity INT NOT NULL,
			status ENUM('FROZEN', 'CONFIRMED', 'CANCELLED') NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NOT NULL COMMENT '过期时间',
			INDEX idx_transaction_id (transaction_id),
			INDEX idx_expires_at (expires_at)
		)`,
		// 秒杀账户表
		`CREATE TABLE IF NOT EXISTS seckill_account (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			user_id BIGINT NOT NULL UNIQUE,
			balance DECIMAL(10,2) NOT NULL DEFAULT 0 COMMENT '可用余额',
			frozen_balance DECIMAL(10,2) NOT NULL DEFAULT 0 COMMENT '冻结余额',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_user_id (user_id)
		)`,
		// 账户冻结记录表
		`CREATE TABLE IF NOT EXISTS seckill_account_freeze (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			transaction_id VARCHAR(64) NOT NULL,
			user_id BIGINT NOT NULL,
			amount DECIMAL(10,2) NOT NULL,
			status ENUM('FROZEN', 'CONFIRMED', 'CANCELLED') NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			expires_at TIMESTAMP NOT NULL COMMENT '过期时间',
			INDEX idx_transaction_id (transaction_id),
			INDEX idx_expires_at (expires_at)
		)`,
		// 秒杀订单表
		`CREATE TABLE IF NOT EXISTS seckill_orders (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			transaction_id VARCHAR(64) NOT NULL,
			user_id BIGINT NOT NULL,
			product_id BIGINT NOT NULL,
			quantity INT NOT NULL,
			price DECIMAL(10,2) NOT NULL,
			total_amount DECIMAL(10,2) NOT NULL,
			status ENUM('PENDING', 'CONFIRMED', 'CANCELLED') NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_transaction_id (transaction_id),
			INDEX idx_user_id (user_id),
			INDEX idx_product_id (product_id)
		)`,
	}

	for _, table := range tables {
		if _, err := db.Exec(table); err != nil {
			return fmt.Errorf("创建表失败: %v", err)
		}
	}

	return nil
}

// 示例：秒杀场景测试
func main() {
	// 连接数据库
	db, err := sql.Open("mysql", "root:password@tcp(localhost:3306)/seckill_db?charset=utf8mb4&parseTime=True&loc=Local")
	if err != nil {
		log.Fatal("连接数据库失败:", err)
	}
	defer db.Close()

	// 初始化数据库表
	if err := initSeckillDatabase(db); err != nil {
		log.Fatal("初始化数据库失败:", err)
	}

	// 初始化测试数据
	initTestData(db)

	// 创建TCC管理器
	tccManager := NewSeckillTCCManager()
	tccManager.AddResource(NewSeckillInventoryResource(db))
	tccManager.AddResource(NewSeckillAccountResource(db))
	tccManager.AddResource(NewSeckillOrderResource(db))

	// 模拟秒杀场景
	ctx := &SeckillTCCContext{
		TransactionID: fmt.Sprintf("seckill_%d", time.Now().UnixNano()),
		UserID:        1001,
		ProductID:     2001,
		Quantity:      1,
		Price:         99.99,
		CreatedAt:     time.Now(),
		Timeout:       30 * time.Second, // 30秒超时
	}

	// 执行秒杀TCC事务
	if err := tccManager.ExecuteSeckillTCC(ctx); err != nil {
		log.Printf("秒杀失败: %v", err)
	} else {
		log.Printf("秒杀成功！")
	}
}

// 初始化测试数据
func initTestData(db *sql.DB) {
	// 初始化商品库存
	db.Exec(`INSERT IGNORE INTO seckill_inventory (product_id, stock) VALUES (2001, 100)`)

	// 初始化用户账户
	db.Exec(`INSERT IGNORE INTO seckill_account (user_id, balance) VALUES (1001, 1000.00)`)

	log.Println("测试数据初始化完成")
}
