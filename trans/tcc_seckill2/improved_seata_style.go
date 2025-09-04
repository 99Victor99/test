package main
package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

// 改进版本：借鉴Seata冻结表设计，保持简单架构

type ResourceManager interface {
	Try(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error
	Confirm(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error
	Cancel(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error
}

// 库存资源管理器 - 使用冻结表模式
type InventoryRM struct {
	db *sql.DB
}

func (rm *InventoryRM) Try(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	txID := args["tx_id"].(string)
	itemID := args["item_id"].(int64)
	quantity := args["quantity"].(int)

	// 1. 幂等检查：查询冻结表
	var existState string
	err := tx.QueryRow(`
		SELECT state FROM inventory_freeze 
		WHERE tx_id = ? AND item_id = ?`, 
		txID, itemID).Scan(&existState)
	
	if err == nil {
		return nil // 已经处理过，幂等返回
	}

	// 2. 检查库存充足性
	var available int
	err = tx.QueryRow(`
		SELECT available FROM seckill_inventory 
		WHERE item_id = ? FOR UPDATE`, 
		itemID).Scan(&available)
	if err != nil {
		return fmt.Errorf("查询库存失败: %v", err)
	}

	if available < quantity {
		return fmt.Errorf("库存不足，可用: %d, 需要: %d", available, quantity)
	}

	// 3. 扣减可用库存
	_, err = tx.Exec(`
		UPDATE seckill_inventory 
		SET available = available - ? 
		WHERE item_id = ?`, 
		quantity, itemID)
	if err != nil {
		return fmt.Errorf("扣减库存失败: %v", err)
	}

	// 4. 记录冻结信息
	_, err = tx.Exec(`
		INSERT INTO inventory_freeze (tx_id, item_id, freeze_quantity, state) 
		VALUES (?, ?, ?, 'TRIED')`, 
		txID, itemID, quantity)
	if err != nil {
		return fmt.Errorf("记录库存冻结失败: %v", err)
	}

	return nil
}

func (rm *InventoryRM) Confirm(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	txID := args["tx_id"].(string)
	itemID := args["item_id"].(int64)
	quantity := args["quantity"].(int)

	// 1. 幂等检查
	var state string
	err := tx.QueryRow(`
		SELECT state FROM inventory_freeze 
		WHERE tx_id = ? AND item_id = ?`, 
		txID, itemID).Scan(&state)
	
	if err != nil || state == "CONFIRMED" {
		return nil // 不存在或已确认，幂等返回
	}

	// 2. 扣减总库存（确认扣款）
	_, err = tx.Exec(`
		UPDATE seckill_inventory 
		SET total = total - ? 
		WHERE item_id = ?`, 
		quantity, itemID)
	if err != nil {
		return fmt.Errorf("确认库存扣减失败: %v", err)
	}

	// 3. 更新冻结状态为已确认
	_, err = tx.Exec(`
		UPDATE inventory_freeze 
		SET state = 'CONFIRMED', update_time = NOW() 
		WHERE tx_id = ? AND item_id = ? AND state = 'TRIED'`, 
		txID, itemID)
	if err != nil {
		return fmt.Errorf("更新库存冻结状态失败: %v", err)
	}

	return nil
}

func (rm *InventoryRM) Cancel(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	txID := args["tx_id"].(string)
	itemID := args["item_id"].(int64)

	// 1. 查询冻结记录
	var freezeQuantity int
	var state string
	err := tx.QueryRow(`
		SELECT freeze_quantity, state FROM inventory_freeze 
		WHERE tx_id = ? AND item_id = ?`, 
		txID, itemID).Scan(&freezeQuantity, &state)
	
	if err != nil || state == "CANCELLED" {
		return nil // 不存在或已取消，幂等返回
	}

	// 2. 恢复可用库存
	_, err = tx.Exec(`
		UPDATE seckill_inventory 
		SET available = available + ? 
		WHERE item_id = ?`, 
		freezeQuantity, itemID)
	if err != nil {
		return fmt.Errorf("恢复库存失败: %v", err)
	}

	// 3. 更新冻结状态为已取消
	_, err = tx.Exec(`
		UPDATE inventory_freeze 
		SET state = 'CANCELLED', update_time = NOW() 
		WHERE tx_id = ? AND item_id = ? AND state IN ('TRIED', 'CONFIRMED')`, 
		txID, itemID)
	if err != nil {
		return fmt.Errorf("更新库存冻结状态失败: %v", err)
	}

	return nil
}

// 账户资源管理器 - 类似的冻结表模式
type AccountRM struct {
	db *sql.DB
}

func (rm *AccountRM) Try(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	txID := args["tx_id"].(string)
	userID := args["user_id"].(int64)
	amount := args["amount"].(float64)

	// 幂等检查
	var existState string
	err := tx.QueryRow(`
		SELECT state FROM account_freeze 
		WHERE tx_id = ? AND user_id = ?`, 
		txID, userID).Scan(&existState)
	
	if err == nil {
		return nil // 已处理过
	}

	// 检查余额并冻结
	var availableBalance float64
	err = tx.QueryRow(`
		SELECT available_balance FROM user_account 
		WHERE user_id = ? FOR UPDATE`, 
		userID).Scan(&availableBalance)
	if err != nil {
		return fmt.Errorf("查询账户余额失败: %v", err)
	}

	if availableBalance < amount {
		return fmt.Errorf("余额不足，可用: %.2f, 需要: %.2f", availableBalance, amount)
	}

	// 扣减可用余额
	_, err = tx.Exec(`
		UPDATE user_account 
		SET available_balance = available_balance - ? 
		WHERE user_id = ?`, 
		amount, userID)
	if err != nil {
		return fmt.Errorf("冻结账户余额失败: %v", err)
	}

	// 记录冻结信息
	_, err = tx.Exec(`
		INSERT INTO account_freeze (tx_id, user_id, freeze_amount, state) 
		VALUES (?, ?, ?, 'TRIED')`, 
		txID, userID, amount)

	return err
}

func (rm *AccountRM) Confirm(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	txID := args["tx_id"].(string)
	userID := args["user_id"].(int64)
	amount := args["amount"].(float64)

	// 幂等检查
	var state string
	err := tx.QueryRow(`
		SELECT state FROM account_freeze 
		WHERE tx_id = ? AND user_id = ?`, 
		txID, userID).Scan(&state)
	
	if err != nil || state == "CONFIRMED" {
		return nil
	}

	// 扣减总余额（确认扣款）
	_, err = tx.Exec(`
		UPDATE user_account 
		SET balance = balance - ? 
		WHERE user_id = ?`, 
		amount, userID)
	if err != nil {
		return fmt.Errorf("确认账户扣款失败: %v", err)
	}

	// 更新冻结状态
	_, err = tx.Exec(`
		UPDATE account_freeze 
		SET state = 'CONFIRMED', update_time = NOW() 
		WHERE tx_id = ? AND user_id = ? AND state = 'TRIED'`, 
		txID, userID)

	return err
}

func (rm *AccountRM) Cancel(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	txID := args["tx_id"].(string)
	userID := args["user_id"].(int64)

	// 查询冻结记录
	var freezeAmount float64
	var state string
	err := tx.QueryRow(`
		SELECT freeze_amount, state FROM account_freeze 
		WHERE tx_id = ? AND user_id = ?`, 
		txID, userID).Scan(&freezeAmount, &state)
	
	if err != nil || state == "CANCELLED" {
		return nil
	}

	// 恢复可用余额
	_, err = tx.Exec(`
		UPDATE user_account 
		SET available_balance = available_balance + ? 
		WHERE user_id = ?`, 
		freezeAmount, userID)
	if err != nil {
		return fmt.Errorf("恢复账户余额失败: %v", err)
	}

	// 更新冻结状态
	_, err = tx.Exec(`
		UPDATE account_freeze 
		SET state = 'CANCELLED', update_time = NOW() 
		WHERE tx_id = ? AND user_id = ? AND state IN ('TRIED', 'CONFIRMED')`, 
		txID, userID)

	return err
}

// 订单资源管理器 - 简单的创建/取消模式
type OrderRM struct {
	db *sql.DB
}

func (rm *OrderRM) Try(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	orderID := args["order_id"].(string)
	txID := args["tx_id"].(string)
	userID := args["user_id"].(int64)
	itemID := args["item_id"].(int64)
	quantity := args["quantity"].(int)
	amount := args["amount"].(float64)

	// 幂等检查：查询是否已存在订单
	var existingStatus string
	err := tx.QueryRow(`
		SELECT status FROM seckill_orders 
		WHERE order_id = ?`, 
		orderID).Scan(&existingStatus)
	
	if err == nil {
		return nil // 订单已存在，幂等返回
	}

	// 创建预订单
	_, err = tx.Exec(`
		INSERT INTO seckill_orders (order_id, tx_id, user_id, item_id, quantity, amount, status) 
		VALUES (?, ?, ?, ?, ?, ?, 'CREATED')`, 
		orderID, txID, userID, itemID, quantity, amount)

	return err
}

func (rm *OrderRM) Confirm(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	// 订单在Try阶段已创建，Confirm阶段无需额外操作
	return nil
}

func (rm *OrderRM) Cancel(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	orderID := args["order_id"].(string)

	// 取消订单
	_, err := tx.Exec(`
		UPDATE seckill_orders 
		SET status = 'CANCELLED' 
		WHERE order_id = ? AND status = 'CREATED'`, 
		orderID)

	return err
}

// 简化的协调器（不需要独立TC服务）
type ImprovedCoordinator struct {
	db        *sql.DB
	resources map[string]ResourceManager
}

func NewImprovedCoordinator(db *sql.DB) *ImprovedCoordinator {
	return &ImprovedCoordinator{
		db: db,
		resources: map[string]ResourceManager{
			"inventory": &InventoryRM{db: db},
			"account":   &AccountRM{db: db},
			"order":     &OrderRM{db: db},
		},
	}
}

func (c *ImprovedCoordinator) StartTransaction(ctx context.Context, txID string, args map[string]interface{}) error {
	// 为args添加事务ID
	args["tx_id"] = txID

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 记录全局事务开始
	_, err = tx.Exec(`
		INSERT INTO tcc_transaction (tx_id, status, timeout_time) 
		VALUES (?, 'TRYING', ?)`, 
		txID, time.Now().Add(30*time.Minute))
	if err != nil {
		return fmt.Errorf("创建全局事务失败: %v", err)
	}

	// Try阶段：调用所有资源管理器
	for resourceName, rm := range c.resources {
		if err := rm.Try(ctx, tx, args); err != nil {
			log.Printf("Resource %s Try failed: %v", resourceName, err)
			return err
		}
	}

	// 更新事务状态为TRIED
	_, err = tx.Exec(`
		UPDATE tcc_transaction 
		SET status = 'TRIED', update_time = NOW() 
		WHERE tx_id = ? AND status = 'TRYING'`, 
		txID)
	if err != nil {
		return fmt.Errorf("更新事务状态失败: %v", err)
	}

	return tx.Commit()
}

func (c *ImprovedCoordinator) Confirm(ctx context.Context, txID string, args map[string]interface{}) error {
	args["tx_id"] = txID

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 更新事务状态为CONFIRMING
	result, err := tx.Exec(`
		UPDATE tcc_transaction 
		SET status = 'CONFIRMING', update_time = NOW() 
		WHERE tx_id = ? AND status = 'TRIED'`, 
		txID)
	if err != nil {
		return err
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("事务状态不正确，无法确认")
	}

	// Confirm阶段：调用所有资源管理器
	for resourceName, rm := range c.resources {
		if err := rm.Confirm(ctx, tx, args); err != nil {
			log.Printf("Resource %s Confirm failed: %v", resourceName, err)
			return err
		}
	}

	// 更新事务状态为CONFIRMED
	_, err = tx.Exec(`
		UPDATE tcc_transaction 
		SET status = 'CONFIRMED', update_time = NOW() 
		WHERE tx_id = ? AND status = 'CONFIRMING'`, 
		txID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (c *ImprovedCoordinator) Cancel(ctx context.Context, txID string, args map[string]interface{}) error {
	args["tx_id"] = txID

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 更新事务状态为CANCELLING
	_, err = tx.Exec(`
		UPDATE tcc_transaction 
		SET status = 'CANCELLING', update_time = NOW() 
		WHERE tx_id = ? AND status IN ('TRYING', 'TRIED', 'CONFIRMING')`, 
		txID)
	if err != nil {
		return err
	}

	// Cancel阶段：调用所有资源管理器
	for resourceName, rm := range c.resources {
		if err := rm.Cancel(ctx, tx, args); err != nil {
			log.Printf("Resource %s Cancel failed: %v", resourceName, err)
			return err
		}
	}

	// 更新事务状态为CANCELLED
	_, err = tx.Exec(`
		UPDATE tcc_transaction 
		SET status = 'CANCELLED', update_time = NOW() 
		WHERE tx_id = ? AND status = 'CANCELLING'`, 
		txID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// 演示函数
func main() {
	// 数据库连接
	db, err := sql.Open("mysql", "user:pass@tcp(localhost:3306)/tcc_demo")
	if err != nil {
		log.Fatal("数据库连接失败:", err)
	}
	defer db.Close()

	coordinator := NewImprovedCoordinator(db)

	// 秒杀场景演示
	txID := uuid.New().String()
	orderID := uuid.New().String()
	
	args := map[string]interface{}{
		"order_id": orderID,
		"user_id":  int64(1001),
		"item_id":  int64(2001),
		"quantity": 2,
		"amount":   299.99,
	}

	fmt.Printf("开始TCC事务: %s\n", txID)

	// Try阶段
	err = coordinator.StartTransaction(context.Background(), txID, args)
	if err != nil {
		fmt.Printf("Try阶段失败: %v\n", err)
		// 尝试回滚
		coordinator.Cancel(context.Background(), txID, args)
		return
	}
	fmt.Println("Try阶段成功")

	// 模拟业务决策（这里假设成功）
	businessSuccess := true

	if businessSuccess {
		// Confirm阶段
		err = coordinator.Confirm(context.Background(), txID, args)
		if err != nil {
			fmt.Printf("Confirm阶段失败: %v\n", err)
			coordinator.Cancel(context.Background(), txID, args)
			return
		}
		fmt.Println("Confirm阶段成功，事务已确认")
	} else {
		// Cancel阶段
		err = coordinator.Cancel(context.Background(), txID, args)
		if err != nil {
			fmt.Printf("Cancel阶段失败: %v\n", err)
			return
		}
		fmt.Println("Cancel阶段成功，事务已取消")
	}

	fmt.Printf("TCC事务完成: %s\n", txID)
}