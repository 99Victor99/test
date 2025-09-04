package main

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// 高并发秒杀TCC上下文
type SeckillDirectTCCContext struct {
	TransactionID string
	UserID        int64
	ProductID     int64
	Quantity      int
	Price         float64
	StartTime     time.Time
}

// TCC事务状态
type TCCTransactionStatus string

const (
	TCCStatusTried     TCCTransactionStatus = "TRIED"
	TCCStatusConfirmed TCCTransactionStatus = "CONFIRMED"
	TCCStatusCancelled TCCTransactionStatus = "CANCELLED"
)

// TCC事务日志
type TCCTransactionLog struct {
	TransactionID string
	Status        TCCTransactionStatus
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TCC资源接口
type DirectTCCResource interface {
	Try(ctx *SeckillDirectTCCContext) error
	Confirm(ctx *SeckillDirectTCCContext) error
	Cancel(ctx *SeckillDirectTCCContext) error
}

// 库存资源 - Try阶段直接扣减
type DirectInventoryResource struct {
	db *sql.DB
	mu sync.RWMutex
}

func NewDirectInventoryResource(db *sql.DB) *DirectInventoryResource {
	return &DirectInventoryResource{db: db}
}

// Try阶段：直接扣减库存（幂等性保证）
func (r *DirectInventoryResource) Try(ctx *SeckillDirectTCCContext) error {
	log.Printf("[库存资源] Try阶段开始 - 事务ID: %s, 商品ID: %d, 数量: %d",
		ctx.TransactionID, ctx.ProductID, ctx.Quantity)

	// 检查是否已经执行过Try操作（防重复执行）
	var count int
	err := r.db.QueryRow(`
		SELECT COUNT(*) FROM inventory_deduct_log 
		WHERE transaction_id = ? AND operation_type IN ('TRY_DEDUCT', 'CONFIRMED', 'CANCELLED')
	`, ctx.TransactionID).Scan(&count)

	if err != nil {
		return fmt.Errorf("检查重复执行失败: %v", err)
	}

	if count > 0 {
		log.Printf("[库存资源] Try阶段已执行过，跳过重复操作")
		return nil // 幂等性：已执行过则直接返回成功
	}

	// 开启事务
	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %v", err)
	}
	defer tx.Rollback()

	// 使用行级锁直接扣减库存
	result, err := tx.Exec(`
		UPDATE seckill_inventory 
		SET stock = stock - ?, 
		    sold_count = sold_count + ?,
		    updated_at = NOW()
		WHERE product_id = ? AND stock >= ? AND status = 'ACTIVE'
	`, ctx.Quantity, ctx.Quantity, ctx.ProductID, ctx.Quantity)

	if err != nil {
		return fmt.Errorf("扣减库存失败: %v", err)
	}

	// 检查是否成功扣减
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("检查扣减结果失败: %v", err)
	}

	if rowsAffected == 0 {
		return errors.New("库存不足或商品不可用")
	}

	// 记录扣减日志
	_, err = tx.Exec(`
		INSERT INTO inventory_deduct_log 
		(transaction_id, product_id, quantity, operation_type, created_at)
		VALUES (?, ?, ?, 'TRY_DEDUCT', NOW())
	`, ctx.TransactionID, ctx.ProductID, ctx.Quantity)

	if err != nil {
		return fmt.Errorf("记录扣减日志失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %v", err)
	}

	log.Printf("[库存资源] Try阶段成功 - 已扣减库存: %d", ctx.Quantity)
	return nil
}

// Confirm阶段：确认扣减（幂等性保证）
func (r *DirectInventoryResource) Confirm(ctx *SeckillDirectTCCContext) error {
	log.Printf("[库存资源] Confirm阶段开始 - 事务ID: %s", ctx.TransactionID)

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %v", err)
	}
	defer tx.Rollback()

	// 检查当前状态，确保幂等性
	var currentType string
	err = tx.QueryRow(`
		SELECT operation_type FROM inventory_deduct_log 
		WHERE transaction_id = ? 
		ORDER BY updated_at DESC LIMIT 1
	`, ctx.TransactionID).Scan(&currentType)

	if err == sql.ErrNoRows {
		return errors.New("未找到Try记录，无法执行Confirm")
	}

	if err != nil {
		return fmt.Errorf("查询操作记录失败: %v", err)
	}

	if currentType == "CONFIRMED" {
		log.Printf("[库存资源] Confirm阶段已执行过，跳过重复操作")
		return nil // 幂等性：已确认则直接返回
	}

	if currentType != "TRY_DEDUCT" {
		return fmt.Errorf("事务状态异常，当前状态: %s", currentType)
	}

	// 更新扣减日志状态为已确认
	_, err = tx.Exec(`
		UPDATE inventory_deduct_log 
		SET operation_type = 'CONFIRMED', updated_at = NOW()
		WHERE transaction_id = ? AND operation_type = 'TRY_DEDUCT'
	`, ctx.TransactionID)

	if err != nil {
		return fmt.Errorf("确认扣减日志失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交确认事务失败: %v", err)
	}

	log.Printf("[库存资源] Confirm阶段成功")
	return nil
}

// Cancel阶段：返还库存（幂等性保证）
func (r *DirectInventoryResource) Cancel(ctx *SeckillDirectTCCContext) error {
	log.Printf("[库存资源] Cancel阶段开始 - 事务ID: %s", ctx.TransactionID)

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %v", err)
	}
	defer tx.Rollback()

	// 检查当前状态和扣减数量
	var currentType string
	var quantity int
	err = tx.QueryRow(`
		SELECT operation_type, quantity FROM inventory_deduct_log 
		WHERE transaction_id = ? 
		ORDER BY updated_at DESC LIMIT 1
	`, ctx.TransactionID).Scan(&currentType, &quantity)

	if err == sql.ErrNoRows {
		log.Printf("[库存资源] Cancel阶段 - 无需补偿（无Try记录）")
		return nil
	}

	if err != nil {
		return fmt.Errorf("查询扣减记录失败: %v", err)
	}

	if currentType == "CANCELLED" {
		log.Printf("[库存资源] Cancel阶段已执行过，跳过重复操作")
		return nil // 幂等性：已取消则直接返回
	}

	if currentType != "TRY_DEDUCT" && currentType != "CONFIRMED" {
		log.Printf("[库存资源] Cancel阶段 - 无需补偿（状态: %s）", currentType)
		return nil
	}

	// 返还库存
	_, err = tx.Exec(`
		UPDATE seckill_inventory 
		SET stock = stock + ?, 
		    sold_count = sold_count - ?,
		    updated_at = NOW()
		WHERE product_id = ?
	`, quantity, quantity, ctx.ProductID)

	if err != nil {
		return fmt.Errorf("返还库存失败: %v", err)
	}

	// 更新补偿日志
	_, err = tx.Exec(`
		UPDATE inventory_deduct_log 
		SET operation_type = 'CANCELLED', updated_at = NOW()
		WHERE transaction_id = ?
	`, ctx.TransactionID)

	if err != nil {
		return fmt.Errorf("更新补偿日志失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交补偿事务失败: %v", err)
	}

	log.Printf("[库存资源] Cancel阶段成功 - 已返还库存: %d", quantity)
	return nil
}

// 账户资源 - Try阶段直接扣减
type DirectAccountResource struct {
	db *sql.DB
	mu sync.RWMutex
}

func NewDirectAccountResource(db *sql.DB) *DirectAccountResource {
	return &DirectAccountResource{db: db}
}

// Try阶段：直接扣减余额（幂等性保证）
func (r *DirectAccountResource) Try(ctx *SeckillDirectTCCContext) error {
	log.Printf("[账户资源] Try阶段开始 - 事务ID: %s, 用户ID: %d, 金额: %.2f",
		ctx.TransactionID, ctx.UserID, ctx.Price)

	// 检查是否已经执行过Try操作
	var count int
	err := r.db.QueryRow(`
		SELECT COUNT(*) FROM account_deduct_log 
		WHERE transaction_id = ? AND operation_type IN ('TRY_DEDUCT', 'CONFIRMED', 'CANCELLED')
	`, ctx.TransactionID).Scan(&count)

	if err != nil {
		return fmt.Errorf("检查重复执行失败: %v", err)
	}

	if count > 0 {
		log.Printf("[账户资源] Try阶段已执行过，跳过重复操作")
		return nil
	}

	totalAmount := ctx.Price * float64(ctx.Quantity)

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %v", err)
	}
	defer tx.Rollback()

	// 直接扣减用户余额
	result, err := tx.Exec(`
		UPDATE user_account 
		SET balance = balance - ?, updated_at = NOW()
		WHERE user_id = ? AND balance >= ? AND status = 'ACTIVE'
	`, totalAmount, ctx.UserID, totalAmount)

	if err != nil {
		return fmt.Errorf("扣减余额失败: %v", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("检查扣减结果失败: %v", err)
	}

	if rowsAffected == 0 {
		return errors.New("余额不足或账户不可用")
	}

	// 记录扣减日志
	_, err = tx.Exec(`
		INSERT INTO account_deduct_log 
		(transaction_id, user_id, amount, operation_type, created_at)
		VALUES (?, ?, ?, 'TRY_DEDUCT', NOW())
	`, ctx.TransactionID, ctx.UserID, totalAmount)

	if err != nil {
		return fmt.Errorf("记录扣减日志失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交事务失败: %v", err)
	}

	log.Printf("[账户资源] Try阶段成功 - 已扣减余额: %.2f", totalAmount)
	return nil
}

// Confirm阶段：确认扣减（幂等性保证）
func (r *DirectAccountResource) Confirm(ctx *SeckillDirectTCCContext) error {
	log.Printf("[账户资源] Confirm阶段开始 - 事务ID: %s", ctx.TransactionID)

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %v", err)
	}
	defer tx.Rollback()

	// 检查当前状态
	var currentType string
	err = tx.QueryRow(`
		SELECT operation_type FROM account_deduct_log 
		WHERE transaction_id = ? 
		ORDER BY updated_at DESC LIMIT 1
	`, ctx.TransactionID).Scan(&currentType)

	if err == sql.ErrNoRows {
		return errors.New("未找到Try记录，无法执行Confirm")
	}

	if err != nil {
		return fmt.Errorf("查询操作记录失败: %v", err)
	}

	if currentType == "CONFIRMED" {
		log.Printf("[账户资源] Confirm阶段已执行过，跳过重复操作")
		return nil
	}

	if currentType != "TRY_DEDUCT" {
		return fmt.Errorf("事务状态异常，当前状态: %s", currentType)
	}

	_, err = tx.Exec(`
		UPDATE account_deduct_log 
		SET operation_type = 'CONFIRMED', updated_at = NOW()
		WHERE transaction_id = ? AND operation_type = 'TRY_DEDUCT'
	`, ctx.TransactionID)

	if err != nil {
		return fmt.Errorf("确认扣减日志失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交确认事务失败: %v", err)
	}

	log.Printf("[账户资源] Confirm阶段成功")
	return nil
}

// Cancel阶段：返还余额（幂等性保证）
func (r *DirectAccountResource) Cancel(ctx *SeckillDirectTCCContext) error {
	log.Printf("[账户资源] Cancel阶段开始 - 事务ID: %s", ctx.TransactionID)

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %v", err)
	}
	defer tx.Rollback()

	// 检查扣减记录
	var currentType string
	var amount float64
	err = tx.QueryRow(`
		SELECT operation_type, amount FROM account_deduct_log 
		WHERE transaction_id = ? 
		ORDER BY updated_at DESC LIMIT 1
	`, ctx.TransactionID).Scan(&currentType, &amount)

	if err == sql.ErrNoRows {
		log.Printf("[账户资源] Cancel阶段 - 无需补偿（无Try记录）")
		return nil
	}

	if err != nil {
		return fmt.Errorf("查询扣减记录失败: %v", err)
	}

	if currentType == "CANCELLED" {
		log.Printf("[账户资源] Cancel阶段已执行过，跳过重复操作")
		return nil
	}

	if currentType != "TRY_DEDUCT" && currentType != "CONFIRMED" {
		log.Printf("[账户资源] Cancel阶段 - 无需补偿（状态: %s）", currentType)
		return nil
	}

	// 返还余额
	_, err = tx.Exec(`
		UPDATE user_account 
		SET balance = balance + ?, updated_at = NOW()
		WHERE user_id = ?
	`, amount, ctx.UserID)

	if err != nil {
		return fmt.Errorf("返还余额失败: %v", err)
	}

	// 更新补偿日志
	_, err = tx.Exec(`
		UPDATE account_deduct_log 
		SET operation_type = 'CANCELLED', updated_at = NOW()
		WHERE transaction_id = ?
	`, ctx.TransactionID)

	if err != nil {
		return fmt.Errorf("更新补偿日志失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交补偿事务失败: %v", err)
	}

	log.Printf("[账户资源] Cancel阶段成功 - 已返还余额: %.2f", amount)
	return nil
}

// 订单资源
type DirectOrderResource struct {
	db *sql.DB
}

func NewDirectOrderResource(db *sql.DB) *DirectOrderResource {
	return &DirectOrderResource{db: db}
}

// Try阶段：创建订单（幂等性保证）
func (r *DirectOrderResource) Try(ctx *SeckillDirectTCCContext) error {
	log.Printf("[订单资源] Try阶段开始 - 事务ID: %s", ctx.TransactionID)

	// 检查订单是否已存在
	var count int
	err := r.db.QueryRow(`
		SELECT COUNT(*) FROM seckill_order 
		WHERE transaction_id = ?
	`, ctx.TransactionID).Scan(&count)

	if err != nil {
		return fmt.Errorf("检查订单重复失败: %v", err)
	}

	if count > 0 {
		log.Printf("[订单资源] Try阶段已执行过，跳过重复操作")
		return nil
	}

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %v", err)
	}
	defer tx.Rollback()

	totalAmount := ctx.Price * float64(ctx.Quantity)

	_, err = tx.Exec(`
		INSERT INTO seckill_order 
		(transaction_id, user_id, product_id, quantity, unit_price, total_amount, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'PENDING', NOW())
	`, ctx.TransactionID, ctx.UserID, ctx.ProductID, ctx.Quantity, ctx.Price, totalAmount)

	if err != nil {
		return fmt.Errorf("创建订单失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交订单事务失败: %v", err)
	}

	log.Printf("[订单资源] Try阶段成功 - 订单已创建")
	return nil
}

// Confirm阶段：确认订单（幂等性保证）
func (r *DirectOrderResource) Confirm(ctx *SeckillDirectTCCContext) error {
	log.Printf("[订单资源] Confirm阶段开始 - 事务ID: %s", ctx.TransactionID)

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %v", err)
	}
	defer tx.Rollback()

	// 检查当前订单状态
	var currentStatus string
	err = tx.QueryRow(`
		SELECT status FROM seckill_order 
		WHERE transaction_id = ?
	`, ctx.TransactionID).Scan(&currentStatus)

	if err == sql.ErrNoRows {
		return errors.New("未找到订单，无法执行Confirm")
	}

	if err != nil {
		return fmt.Errorf("查询订单状态失败: %v", err)
	}

	if currentStatus == "CONFIRMED" {
		log.Printf("[订单资源] Confirm阶段已执行过，跳过重复操作")
		return nil
	}

	if currentStatus != "PENDING" {
		return fmt.Errorf("订单状态异常，当前状态: %s", currentStatus)
	}

	_, err = tx.Exec(`
		UPDATE seckill_order 
		SET status = 'CONFIRMED', updated_at = NOW()
		WHERE transaction_id = ?
	`, ctx.TransactionID)

	if err != nil {
		return fmt.Errorf("确认订单失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交确认事务失败: %v", err)
	}

	log.Printf("[订单资源] Confirm阶段成功")
	return nil
}

// Cancel阶段：取消订单（幂等性保证）
func (r *DirectOrderResource) Cancel(ctx *SeckillDirectTCCContext) error {
	log.Printf("[订单资源] Cancel阶段开始 - 事务ID: %s", ctx.TransactionID)

	tx, err := r.db.Begin()
	if err != nil {
		return fmt.Errorf("开启事务失败: %v", err)
	}
	defer tx.Rollback()

	// 检查当前订单状态
	var currentStatus string
	err = tx.QueryRow(`
		SELECT status FROM seckill_order 
		WHERE transaction_id = ?
	`, ctx.TransactionID).Scan(&currentStatus)

	if err == sql.ErrNoRows {
		log.Printf("[订单资源] Cancel阶段 - 无需补偿（无订单记录）")
		return nil
	}

	if err != nil {
		return fmt.Errorf("查询订单状态失败: %v", err)
	}

	if currentStatus == "CANCELLED" {
		log.Printf("[订单资源] Cancel阶段已执行过，跳过重复操作")
		return nil
	}

	_, err = tx.Exec(`
		UPDATE seckill_order 
		SET status = 'CANCELLED', updated_at = NOW()
		WHERE transaction_id = ?
	`, ctx.TransactionID)

	if err != nil {
		return fmt.Errorf("取消订单失败: %v", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("提交取消事务失败: %v", err)
	}

	log.Printf("[订单资源] Cancel阶段成功")
	return nil
}

// 高并发秒杀TCC管理器（带恢复机制）
type SeckillDirectTCCManager struct {
	resources []DirectTCCResource
	db        *sql.DB
	mu        sync.RWMutex
}

func NewSeckillDirectTCCManager(db *sql.DB) *SeckillDirectTCCManager {
	return &SeckillDirectTCCManager{
		resources: []DirectTCCResource{
			NewDirectInventoryResource(db),
			NewDirectAccountResource(db),
			NewDirectOrderResource(db),
		},
		db: db,
	}
}

// 记录TCC事务日志
func (stm *SeckillDirectTCCManager) logTCCTransaction(transactionID string, status TCCTransactionStatus) error {
	_, err := stm.db.Exec(`
		INSERT INTO tcc_transaction_log (transaction_id, status, created_at, updated_at)
		VALUES (?, ?, NOW(), NOW())
		ON DUPLICATE KEY UPDATE status = ?, updated_at = NOW()
	`, transactionID, status, status)
	return err
}

// 执行秒杀事务（带防重复执行）
func (stm *SeckillDirectTCCManager) ExecuteSeckill(ctx *SeckillDirectTCCContext) error {
	log.Printf("[秒杀TCC] 开始执行秒杀事务: %s", ctx.TransactionID)
	ctx.StartTime = time.Now()

	// 检查事务是否已经完成（防重复执行）
	var status string
	err := stm.db.QueryRow(`
		SELECT status FROM tcc_transaction_log 
		WHERE transaction_id = ?
	`, ctx.TransactionID).Scan(&status)

	if err == nil {
		if status == string(TCCStatusConfirmed) {
			log.Printf("[秒杀TCC] 事务已完成，跳过重复执行: %s", ctx.TransactionID)
			return nil
		}
		if status == string(TCCStatusCancelled) {
			log.Printf("[秒杀TCC] 事务已取消，跳过重复执行: %s", ctx.TransactionID)
			return errors.New("事务已取消")
		}
	}

	// Try阶段：直接扣减资源
	if err := stm.tryResources(ctx); err != nil {
		log.Printf("[秒杀TCC] Try阶段失败: %v", err)
		stm.logTCCTransaction(ctx.TransactionID, TCCStatusCancelled)
		stm.cancelResources(ctx)
		return fmt.Errorf("秒杀失败: %v", err)
	}

	// 记录Try成功状态
	if err := stm.logTCCTransaction(ctx.TransactionID, TCCStatusTried); err != nil {
		log.Printf("[秒杀TCC] 记录Try状态失败: %v", err)
	}

	// Confirm阶段：确认所有操作
	if err := stm.confirmResources(ctx); err != nil {
		log.Printf("[秒杀TCC] Confirm阶段失败: %v", err)
		stm.logTCCTransaction(ctx.TransactionID, TCCStatusCancelled)
		stm.cancelResources(ctx)
		return fmt.Errorf("确认失败: %v", err)
	}

	// 记录Confirm成功状态
	if err := stm.logTCCTransaction(ctx.TransactionID, TCCStatusConfirmed); err != nil {
		log.Printf("[秒杀TCC] 记录Confirm状态失败: %v", err)
	}

	duration := time.Since(ctx.StartTime)
	log.Printf("[秒杀TCC] 秒杀事务成功完成: %s, 耗时: %v", ctx.TransactionID, duration)
	return nil
}

// Try阶段：尝试所有资源操作（带状态跟踪）
func (stm *SeckillDirectTCCManager) tryResources(ctx *SeckillDirectTCCContext) error {
	log.Printf("[秒杀TCC] 开始Try阶段")
	for i, resource := range stm.resources {
		if err := resource.Try(ctx); err != nil {
			log.Printf("[秒杀TCC] Try失败，资源%d: %v", i, err)
			// 补偿已成功的资源
			for j := i - 1; j >= 0; j-- {
				if cancelErr := stm.resources[j].Cancel(ctx); cancelErr != nil {
					log.Printf("[秒杀TCC] 补偿失败，资源%d: %v", j, cancelErr)
				} else {
					stm.markResourceCancelCompleted(ctx.TransactionID, j)
				}
			}
			return err
		}
		// 标记Try成功
		stm.markResourceTryCompleted(ctx.TransactionID, i)
	}
	return nil
}

// Confirm阶段：确认所有资源操作（带状态跟踪）
func (stm *SeckillDirectTCCManager) confirmResources(ctx *SeckillDirectTCCContext) error {
	log.Printf("[秒杀TCC] 开始Confirm阶段")
	for i, resource := range stm.resources {
		if err := resource.Confirm(ctx); err != nil {
			log.Printf("[秒杀TCC] Confirm失败，资源%d: %v", i, err)
			return err
		}
		// 标记Confirm成功
		stm.markResourceConfirmCompleted(ctx.TransactionID, i)
	}
	return nil
}

// Cancel阶段：取消所有资源操作（带状态跟踪）
func (stm *SeckillDirectTCCManager) cancelResources(ctx *SeckillDirectTCCContext) {
	log.Printf("[秒杀TCC] 开始Cancel补偿操作")
	for i, resource := range stm.resources {
		if err := resource.Cancel(ctx); err != nil {
			log.Printf("[秒杀TCC] Cancel补偿失败，资源%d: %v", i, err)
		} else {
			// 标记Cancel成功
			stm.markResourceCancelCompleted(ctx.TransactionID, i)
		}
	}
}

// TCC资源状态跟踪
type TCCResourceStatus struct {
	TransactionID string
	ResourceType  string // "inventory", "account", "order"
	ResourceIndex int    // 资源在数组中的索引
	Phase         string // "try", "confirm", "cancel"
	Status        string // "pending", "completed", "failed"
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// 恢复机制：处理系统重启后的未完成事务
func (stm *SeckillDirectTCCManager) RecoverTransactions() error {
	log.Printf("[恢复机制] 开始恢复未完成的TCC事务")

	// 查询所有未完成的事务
	rows, err := stm.db.Query(`
		SELECT DISTINCT transaction_id FROM tcc_transaction_log 
		WHERE status IN ('TRIED', 'CONFIRMED', 'CANCELLED')
		ORDER BY created_at ASC
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var transactionID string
		if err := rows.Scan(&transactionID); err != nil {
			continue
		}

		// 分析每个事务的具体执行状态
		if err := stm.recoverSingleTransaction(transactionID); err != nil {
			log.Printf("[恢复机制] 恢复事务失败: %s, %v", transactionID, err)
		}
	}
	return nil
}

// 恢复单个事务
func (stm *SeckillDirectTCCManager) recoverSingleTransaction(transactionID string) error {
	// 获取事务的主状态
	var mainStatus string
	err := stm.db.QueryRow(`
		SELECT status FROM tcc_transaction_log 
		WHERE transaction_id = ?
	`, transactionID).Scan(&mainStatus)
	if err != nil {
		return err
	}

	ctx, err := stm.buildRecoveryContext(transactionID)
	if err != nil {
		return err
	}

	switch mainStatus {
	case "TRIED":
		// Try阶段可能部分完成，需要检查每个资源状态
		return stm.recoverFromTryPhase(ctx)
	case "CONFIRMED":
		// Confirm阶段可能部分完成，继续完成剩余资源
		return stm.recoverFromConfirmPhase(ctx)
	case "CANCELLED":
		// Cancel阶段可能部分完成，继续完成剩余补偿
		return stm.recoverFromCancelPhase(ctx)
	}
	return nil
}

// 从Try阶段恢复
func (stm *SeckillDirectTCCManager) recoverFromTryPhase(ctx *SeckillDirectTCCContext) error {
	log.Printf("[恢复机制] 从Try阶段恢复: %s", ctx.TransactionID)
	
	// 检查Try阶段每个资源的执行状态
	for i, resource := range stm.resources {
		if !stm.isResourceTryCompleted(ctx.TransactionID, i) {
			// 该资源的Try未完成，继续执行
			log.Printf("[恢复机制] 继续执行资源%d的Try: %s", i, ctx.TransactionID)
			if err := resource.Try(ctx); err != nil {
				// Try失败，需要对已完成的资源执行Cancel
				log.Printf("[恢复机制] Try失败，执行补偿: %s, %v", ctx.TransactionID, err)
				stm.logTCCTransaction(ctx.TransactionID, TCCStatusCancelled)
				return stm.recoverFromCancelPhase(ctx)
			}
			stm.markResourceTryCompleted(ctx.TransactionID, i)
		}
	}

	// 所有Try完成，尝试Confirm
	log.Printf("[恢复机制] Try阶段恢复完成，开始Confirm: %s", ctx.TransactionID)
	stm.logTCCTransaction(ctx.TransactionID, TCCStatusConfirmed)
	return stm.recoverFromConfirmPhase(ctx)
}

// 从Confirm阶段恢复
func (stm *SeckillDirectTCCManager) recoverFromConfirmPhase(ctx *SeckillDirectTCCContext) error {
	log.Printf("[恢复机制] 从Confirm阶段恢复: %s", ctx.TransactionID)
	
	// 检查Confirm阶段每个资源的执行状态
	for i, resource := range stm.resources {
		if !stm.isResourceConfirmCompleted(ctx.TransactionID, i) {
			log.Printf("[恢复机制] 继续执行资源%d的Confirm: %s", i, ctx.TransactionID)
			if err := resource.Confirm(ctx); err != nil {
				log.Printf("[恢复机制] Confirm失败: %s, %v", ctx.TransactionID, err)
				// Confirm失败通常意味着数据不一致，需要人工介入
				return err
			}
			stm.markResourceConfirmCompleted(ctx.TransactionID, i)
		}
	}
	log.Printf("[恢复机制] Confirm阶段恢复完成: %s", ctx.TransactionID)
	return nil
}

// 从Cancel阶段恢复
func (stm *SeckillDirectTCCManager) recoverFromCancelPhase(ctx *SeckillDirectTCCContext) error {
	log.Printf("[恢复机制] 从Cancel阶段恢复: %s", ctx.TransactionID)
	
	// 检查Cancel阶段每个资源的执行状态
	for i, resource := range stm.resources {
		if !stm.isResourceCancelCompleted(ctx.TransactionID, i) {
			log.Printf("[恢复机制] 继续执行资源%d的Cancel: %s", i, ctx.TransactionID)
			resource.Cancel(ctx) // Cancel通常不返回错误，基于幂等性
			stm.markResourceCancelCompleted(ctx.TransactionID, i)
		}
	}
	log.Printf("[恢复机制] Cancel阶段恢复完成: %s", ctx.TransactionID)
	return nil
}

// 检查资源Try状态
func (stm *SeckillDirectTCCManager) isResourceTryCompleted(transactionID string, resourceIndex int) bool {
	var count int
	stm.db.QueryRow(`
		SELECT COUNT(*) FROM tcc_resource_status 
		WHERE transaction_id = ? AND resource_index = ? AND phase = 'try' AND status = 'completed'
	`, transactionID, resourceIndex).Scan(&count)
	return count > 0
}

// 检查资源Confirm状态
func (stm *SeckillDirectTCCManager) isResourceConfirmCompleted(transactionID string, resourceIndex int) bool {
	var count int
	stm.db.QueryRow(`
		SELECT COUNT(*) FROM tcc_resource_status 
		WHERE transaction_id = ? AND resource_index = ? AND phase = 'confirm' AND status = 'completed'
	`, transactionID, resourceIndex).Scan(&count)
	return count > 0
}

// 检查资源Cancel状态
func (stm *SeckillDirectTCCManager) isResourceCancelCompleted(transactionID string, resourceIndex int) bool {
	var count int
	stm.db.QueryRow(`
		SELECT COUNT(*) FROM tcc_resource_status 
		WHERE transaction_id = ? AND resource_index = ? AND phase = 'cancel' AND status = 'completed'
	`, transactionID, resourceIndex).Scan(&count)
	return count > 0
}

// 标记资源Try完成
func (stm *SeckillDirectTCCManager) markResourceTryCompleted(transactionID string, resourceIndex int) {
	resourceTypes := []string{"inventory", "account", "order"}
	resourceType := resourceTypes[resourceIndex]
	
	stm.db.Exec(`
		INSERT INTO tcc_resource_status 
		(transaction_id, resource_type, resource_index, phase, status, created_at, updated_at)
		VALUES (?, ?, ?, 'try', 'completed', NOW(), NOW())
		ON DUPLICATE KEY UPDATE status = 'completed', updated_at = NOW()
	`, transactionID, resourceType, resourceIndex)
}

// 标记资源Confirm完成
func (stm *SeckillDirectTCCManager) markResourceConfirmCompleted(transactionID string, resourceIndex int) {
	resourceTypes := []string{"inventory", "account", "order"}
	resourceType := resourceTypes[resourceIndex]
	
	stm.db.Exec(`
		INSERT INTO tcc_resource_status 
		(transaction_id, resource_type, resource_index, phase, status, created_at, updated_at)
		VALUES (?, ?, ?, 'confirm', 'completed', NOW(), NOW())
		ON DUPLICATE KEY UPDATE status = 'completed', updated_at = NOW()
	`, transactionID, resourceType, resourceIndex)
}

// 标记资源Cancel完成
func (stm *SeckillDirectTCCManager) markResourceCancelCompleted(transactionID string, resourceIndex int) {
	resourceTypes := []string{"inventory", "account", "order"}
	resourceType := resourceTypes[resourceIndex]
	
	stm.db.Exec(`
		INSERT INTO tcc_resource_status 
		(transaction_id, resource_type, resource_index, phase, status, created_at, updated_at)
		VALUES (?, ?, ?, 'cancel', 'completed', NOW(), NOW())
		ON DUPLICATE KEY UPDATE status = 'completed', updated_at = NOW()
	`, transactionID, resourceType, resourceIndex)
}

// 构建恢复上下文
func (stm *SeckillDirectTCCManager) buildRecoveryContext(transactionID string) (*SeckillDirectTCCContext, error) {
	// 从订单表获取事务详情
	var userID, productID int64
	var quantity int
	var unitPrice float64

	err := stm.db.QueryRow(`
		SELECT user_id, product_id, quantity, unit_price 
		FROM seckill_order 
		WHERE transaction_id = ?
	`, transactionID).Scan(&userID, &productID, &quantity, &unitPrice)

	if err != nil {
		return nil, fmt.Errorf("获取订单信息失败: %v", err)
	}

	return &SeckillDirectTCCContext{
		TransactionID: transactionID,
		UserID:        userID,
		ProductID:     productID,
		Quantity:      quantity,
		Price:         unitPrice,
		StartTime:     time.Now(),
	}, nil
}

// 初始化数据库表结构（包含TCC事务日志表）
func initDirectSeckillDatabase(db *sql.DB) error {
	tables := []string{
		// TCC事务日志表
		`CREATE TABLE IF NOT EXISTS tcc_transaction_log (
			transaction_id VARCHAR(64) PRIMARY KEY,
			status ENUM('TRIED', 'CONFIRMED', 'CANCELLED') NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_status (status),
			INDEX idx_created_at (created_at)
		)`,
		// TCC资源状态跟踪表
		`CREATE TABLE IF NOT EXISTS tcc_resource_status (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			transaction_id VARCHAR(64) NOT NULL,
			resource_type VARCHAR(32) NOT NULL COMMENT 'inventory/account/order',
			resource_index INT NOT NULL COMMENT '资源在数组中的索引',
			phase VARCHAR(16) NOT NULL COMMENT 'try/confirm/cancel',
			status VARCHAR(16) NOT NULL COMMENT 'pending/completed/failed',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			UNIQUE KEY uk_transaction_resource_phase (transaction_id, resource_index, phase),
			INDEX idx_transaction_id (transaction_id),
			INDEX idx_status (status)
		)`,
		// 秒杀库存表
		`CREATE TABLE IF NOT EXISTS seckill_inventory (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			product_id BIGINT NOT NULL UNIQUE,
			product_name VARCHAR(255) NOT NULL,
			stock INT NOT NULL DEFAULT 0 COMMENT '当前库存',
			sold_count INT NOT NULL DEFAULT 0 COMMENT '已售数量',
			original_stock INT NOT NULL DEFAULT 0 COMMENT '原始库存',
			price DECIMAL(10,2) NOT NULL,
			status ENUM('ACTIVE', 'INACTIVE') DEFAULT 'ACTIVE',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_product_id (product_id),
			INDEX idx_status (status)
		)`,
		// 库存扣减日志表
		`CREATE TABLE IF NOT EXISTS inventory_deduct_log (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			transaction_id VARCHAR(64) NOT NULL,
			product_id BIGINT NOT NULL,
			quantity INT NOT NULL,
			operation_type ENUM('TRY_DEDUCT', 'CONFIRMED', 'CANCELLED') NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_transaction_id (transaction_id),
			INDEX idx_product_id (product_id)
		)`,
		// 用户账户表
		`CREATE TABLE IF NOT EXISTS user_account (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			user_id BIGINT NOT NULL UNIQUE,
			username VARCHAR(100) NOT NULL,
			balance DECIMAL(15,2) NOT NULL DEFAULT 0.00,
			status ENUM('ACTIVE', 'FROZEN', 'INACTIVE') DEFAULT 'ACTIVE',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_user_id (user_id),
			INDEX idx_status (status)
		)`,
		// 账户扣减日志表
		`CREATE TABLE IF NOT EXISTS account_deduct_log (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			transaction_id VARCHAR(64) NOT NULL,
			user_id BIGINT NOT NULL,
			amount DECIMAL(15,2) NOT NULL,
			operation_type ENUM('TRY_DEDUCT', 'CONFIRMED', 'CANCELLED') NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_transaction_id (transaction_id),
			INDEX idx_user_id (user_id)
		)`,
		// 秒杀订单表
		`CREATE TABLE IF NOT EXISTS seckill_order (
			id BIGINT PRIMARY KEY AUTO_INCREMENT,
			transaction_id VARCHAR(64) NOT NULL UNIQUE,
			user_id BIGINT NOT NULL,
			product_id BIGINT NOT NULL,
			quantity INT NOT NULL,
			unit_price DECIMAL(10,2) NOT NULL,
			total_amount DECIMAL(15,2) NOT NULL,
			status ENUM('PENDING', 'CONFIRMED', 'CANCELLED') DEFAULT 'PENDING',
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
			INDEX idx_transaction_id (transaction_id),
			INDEX idx_user_id (user_id),
			INDEX idx_product_id (product_id),
			INDEX idx_status (status)
		)`,
	}

	for _, table := range tables {
		if _, err := db.Exec(table); err != nil {
			return fmt.Errorf("创建表失败: %v", err)
		}
	}

	log.Println("数据库表初始化完成")
	return nil
}

// 初始化测试数据
func initDirectSeckillTestData(db *sql.DB) error {
	// 插入测试商品
	_, err := db.Exec(`
		INSERT IGNORE INTO seckill_inventory 
		(product_id, product_name, stock, original_stock, price, status)
		VALUES 
		(1001, 'iPhone 15 Pro', 100, 100, 8999.00, 'ACTIVE'),
		(1002, 'MacBook Pro', 50, 50, 15999.00, 'ACTIVE'),
		(1003, 'AirPods Pro', 200, 200, 1999.00, 'ACTIVE')
	`)
	if err != nil {
		return fmt.Errorf("插入测试商品失败: %v", err)
	}

	// 插入测试用户
	_, err = db.Exec(`
		INSERT IGNORE INTO user_account 
		(user_id, username, balance, status)
		VALUES 
		(10001, 'user001', 50000.00, 'ACTIVE'),
		(10002, 'user002', 30000.00, 'ACTIVE'),
		(10003, 'user003', 20000.00, 'ACTIVE'),
		(10004, 'user004', 100000.00, 'ACTIVE'),
		(10005, 'user005', 80000.00, 'ACTIVE')
	`)
	if err != nil {
		return fmt.Errorf("插入测试用户失败: %v", err)
	}

	log.Println("测试数据初始化完成")
	return nil
}

// 高并发测试函数
func runConcurrentSeckillTest(manager *SeckillDirectTCCManager, concurrency int) {
	log.Printf("开始高并发秒杀测试，并发数: %d", concurrency)

	var wg sync.WaitGroup
	successCount := int64(0)
	failCount := int64(0)

	startTime := time.Now()

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			ctx := &SeckillDirectTCCContext{
				TransactionID: fmt.Sprintf("seckill_%d_%d", time.Now().UnixNano(), index),
				UserID:        int64(10001 + index%5), // 轮询使用5个测试用户
				ProductID:     1001,                   // iPhone 15 Pro
				Quantity:      1,
				Price:         8999.00,
			}

			if err := manager.ExecuteSeckill(ctx); err != nil {
				log.Printf("秒杀失败[%d]: %v", index, err)
				failCount++
			} else {
				log.Printf("秒杀成功[%d]: %s", index, ctx.TransactionID)
				successCount++
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(startTime)

	log.Printf("高并发秒杀测试完成:")
	log.Printf("- 总并发数: %d", concurrency)
	log.Printf("- 成功数: %d", successCount)
	log.Printf("- 失败数: %d", failCount)
	log.Printf("- 成功率: %.2f%%", float64(successCount)/float64(concurrency)*100)
	log.Printf("- 总耗时: %v", duration)
	log.Printf("- 平均TPS: %.2f", float64(concurrency)/duration.Seconds())
}

// 主函数
func main() {
	// 连接数据库
	db, err := sql.Open("mysql", "root:password@tcp(localhost:3306)/seckill_db?charset=utf8mb4&parseTime=True&loc=Local")
	if err != nil {
		log.Fatal("连接数据库失败:", err)
	}
	defer db.Close()

	// 设置连接池参数（高并发优化）
	db.SetMaxOpenConns(100)
	db.SetMaxIdleConns(20)
	db.SetConnMaxLifetime(time.Hour)

	// 初始化数据库
	if err := initDirectSeckillDatabase(db); err != nil {
		log.Fatal("初始化数据库失败:", err)
	}

	// 初始化测试数据
	if err := initDirectSeckillTestData(db); err != nil {
		log.Fatal("初始化测试数据失败:", err)
	}

	// 创建TCC管理器
	manager := NewSeckillDirectTCCManager(db)

	// 系统启动时执行恢复机制
	log.Println("\n=== 系统启动恢复机制 ===")
	if err := manager.RecoverTransactions(); err != nil {
		log.Printf("恢复机制执行失败: %v", err)
	}

	// 单个秒杀测试
	log.Println("\n=== 单个秒杀测试 ===")
	singleCtx := &SeckillDirectTCCContext{
		TransactionID: fmt.Sprintf("single_test_%d", time.Now().UnixNano()),
		UserID:        10001,
		ProductID:     1001,
		Quantity:      1,
		Price:         8999.00,
	}

	if err := manager.ExecuteSeckill(singleCtx); err != nil {
		log.Printf("单个秒杀测试失败: %v", err)
	} else {
		log.Printf("单个秒杀测试成功: %s", singleCtx.TransactionID)
	}

	// 高并发秒杀测试
	log.Println("\n=== 高并发秒杀测试 ===")
	runConcurrentSeckillTest(manager, 50) // 50个并发

	log.Println("\n秒杀TCC测试完成")
}
