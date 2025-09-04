package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"

	_ "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
)

// 带分支表的版本 - 传统TCC设计

type ResourceManagerWithBranch interface {
	Try(ctx context.Context, tx *sql.Tx, branchID int64, args map[string]interface{}) error
	Confirm(ctx context.Context, tx *sql.Tx, branchID int64, args map[string]interface{}) error
	Cancel(ctx context.Context, tx *sql.Tx, branchID int64, args map[string]interface{}) error
}

type InventoryRMWithBranch struct{}

func (rm *InventoryRMWithBranch) Try(ctx context.Context, tx *sql.Tx, branchID int64, args map[string]interface{}) error {
	txID := args["tx_id"].(string)
	itemID := args["item_id"].(int64)
	quantity := args["quantity"].(int)

	// 1. 检查分支表状态（幂等性）
	var status string
	err := tx.QueryRow(`
		SELECT status FROM tcc_branch 
		WHERE tx_id = ? AND resource_type = 'inventory' AND resource_id = ?`, 
		txID, fmt.Sprintf("%d", itemID)).Scan(&status)
	
	if err == nil {
		if status == "PREPARED" {
			return nil // 已经准备过了，幂等返回
		}
		return fmt.Errorf("分支状态异常: %s", status)
	}

	// 2. 执行业务逻辑
	var available int
	err = tx.QueryRow(`
		SELECT available FROM seckill_inventory 
		WHERE item_id = ? FOR UPDATE`, 
		itemID).Scan(&available)
	if err != nil {
		return fmt.Errorf("查询库存失败: %v", err)
	}

	if available < quantity {
		return fmt.Errorf("库存不足")
	}

	// 扣减可用库存
	_, err = tx.Exec(`
		UPDATE seckill_inventory 
		SET available = available - ? 
		WHERE item_id = ?`, 
		quantity, itemID)
	if err != nil {
		return err
	}

	// 记录到冻结表（具体补偿数据）
	_, err = tx.Exec(`
		INSERT INTO inventory_freeze (tx_id, item_id, freeze_quantity, state) 
		VALUES (?, ?, ?, 'TRIED')`, 
		txID, itemID, quantity)
	if err != nil {
		return err
	}

	// 3. 记录到分支表（分支状态管理）
	_, err = tx.Exec(`
		INSERT INTO tcc_branch (branch_id, tx_id, resource_type, resource_id, status) 
		VALUES (?, ?, 'inventory', ?, 'PREPARED')`, 
		branchID, txID, fmt.Sprintf("%d", itemID))

	return err
}

func (rm *InventoryRMWithBranch) Confirm(ctx context.Context, tx *sql.Tx, branchID int64, args map[string]interface{}) error {
	txID := args["tx_id"].(string)
	itemID := args["item_id"].(int64)
	quantity := args["quantity"].(int)

	// 1. 检查分支状态
	var status string
	err := tx.QueryRow(`
		SELECT status FROM tcc_branch 
		WHERE branch_id = ? AND tx_id = ?`, 
		branchID, txID).Scan(&status)
	
	if err != nil || status == "COMMITTED" {
		return nil // 幂等
	}

	if status != "PREPARED" {
		return fmt.Errorf("分支状态不正确: %s", status)
	}

	// 2. 执行业务Confirm
	_, err = tx.Exec(`
		UPDATE seckill_inventory 
		SET total = total - ? 
		WHERE item_id = ?`, 
		quantity, itemID)
	if err != nil {
		return err
	}

	// 更新冻结表
	_, err = tx.Exec(`
		UPDATE inventory_freeze 
		SET state = 'CONFIRMED' 
		WHERE tx_id = ? AND item_id = ?`, 
		txID, itemID)
	if err != nil {
		return err
	}

	// 3. 更新分支状态
	_, err = tx.Exec(`
		UPDATE tcc_branch 
		SET status = 'COMMITTED', update_time = NOW() 
		WHERE branch_id = ? AND tx_id = ? AND status = 'PREPARED'`, 
		branchID, txID)

	return err
}

func (rm *InventoryRMWithBranch) Cancel(ctx context.Context, tx *sql.Tx, branchID int64, args map[string]interface{}) error {
	txID := args["tx_id"].(string)
	itemID := args["item_id"].(int64)

	// 1. 检查分支状态
	var status string
	err := tx.QueryRow(`
		SELECT status FROM tcc_branch 
		WHERE branch_id = ? AND tx_id = ?`, 
		branchID, txID).Scan(&status)
	
	if err != nil || status == "CANCELLED" {
		return nil // 幂等
	}

	// 2. 从冻结表获取补偿数据
	var freezeQuantity int
	err = tx.QueryRow(`
		SELECT freeze_quantity FROM inventory_freeze 
		WHERE tx_id = ? AND item_id = ?`, 
		txID, itemID).Scan(&freezeQuantity)
	if err != nil {
		return err
	}

	// 3. 执行业务Cancel
	_, err = tx.Exec(`
		UPDATE seckill_inventory 
		SET available = available + ? 
		WHERE item_id = ?`, 
		freezeQuantity, itemID)
	if err != nil {
		return err
	}

	// 更新冻结表
	_, err = tx.Exec(`
		UPDATE inventory_freeze 
		SET state = 'CANCELLED' 
		WHERE tx_id = ? AND item_id = ?`, 
		txID, itemID)
	if err != nil {
		return err
	}

	// 4. 更新分支状态
	_, err = tx.Exec(`
		UPDATE tcc_branch 
		SET status = 'CANCELLED', update_time = NOW() 
		WHERE branch_id = ? AND tx_id = ?`, 
		branchID, txID)

	return err
}

// 带分支表的协调器
type CoordinatorWithBranch struct {
	db        *sql.DB
	resources map[string]ResourceManagerWithBranch
}

func (c *CoordinatorWithBranch) StartTransaction(ctx context.Context, txID string, args map[string]interface{}) error {
	args["tx_id"] = txID

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. 创建全局事务
	_, err = tx.Exec(`
		INSERT INTO tcc_transaction (tx_id, status) 
		VALUES (?, 'TRYING')`, txID)
	if err != nil {
		return err
	}

	// 2. 为每个资源创建分支并执行Try
	branchCounter := int64(1)
	for resourceName, rm := range c.resources {
		branchID := branchCounter
		branchCounter++

		err = rm.Try(ctx, tx, branchID, args)
		if err != nil {
			log.Printf("Resource %s Try failed: %v", resourceName, err)
			return err
		}
	}

	// 3. 更新全局事务状态
	_, err = tx.Exec(`
		UPDATE tcc_transaction 
		SET status = 'TRIED', update_time = NOW() 
		WHERE tx_id = ?`, txID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (c *CoordinatorWithBranch) Confirm(ctx context.Context, txID string, args map[string]interface{}) error {
	args["tx_id"] = txID

	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. 更新全局事务状态
	_, err = tx.Exec(`
		UPDATE tcc_transaction 
		SET status = 'CONFIRMING', update_time = NOW() 
		WHERE tx_id = ? AND status = 'TRIED'`, txID)
	if err != nil {
		return err
	}

	// 2. 查询所有分支并执行Confirm
	rows, err := tx.Query(`
		SELECT branch_id, resource_type 
		FROM tcc_branch 
		WHERE tx_id = ? AND status = 'PREPARED'`, txID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var branchID int64
		var resourceType string
		err = rows.Scan(&branchID, &resourceType)
		if err != nil {
			return err
		}

		if rm, exists := c.resources[resourceType]; exists {
			err = rm.Confirm(ctx, tx, branchID, args)
			if err != nil {
				return err
			}
		}
	}

	// 3. 更新全局事务状态
	_, err = tx.Exec(`
		UPDATE tcc_transaction 
		SET status = 'CONFIRMED', update_time = NOW() 
		WHERE tx_id = ?`, txID)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// 演示：分支表的查询和监控功能
func (c *CoordinatorWithBranch) QueryTransactionStatus(ctx context.Context, txID string) error {
	// 查询全局事务状态
	var globalStatus string
	err := c.db.QueryRow(`
		SELECT status FROM tcc_transaction WHERE tx_id = ?`, 
		txID).Scan(&globalStatus)
	if err != nil {
		return err
	}

	fmt.Printf("全局事务 %s 状态: %s\n", txID, globalStatus)

	// 查询分支详情
	rows, err := c.db.Query(`
		SELECT branch_id, resource_type, resource_id, status, create_time 
		FROM tcc_branch 
		WHERE tx_id = ? 
		ORDER BY branch_id`, txID)
	if err != nil {
		return err
	}
	defer rows.Close()

	fmt.Println("分支事务详情:")
	for rows.Next() {
		var branchID int64
		var resourceType, resourceID, status, createTime string
		err = rows.Scan(&branchID, &resourceType, &resourceID, &status, &createTime)
		if err != nil {
			return err
		}
		fmt.Printf("  分支 %d: %s[%s] = %s (创建时间: %s)\n", 
			branchID, resourceType, resourceID, status, createTime)
	}

	return nil
}

func main() {
	fmt.Println("带分支表的TCC实现演示")
	
	// 这个版本同时维护：
	// 1. tcc_transaction - 全局事务状态
	// 2. tcc_branch - 分支事务状态  
	// 3. *_freeze - 具体的补偿数据
	
	fmt.Println("优点：")
	fmt.Println("- 统一的分支状态管理")
	fmt.Println("- 便于监控和查询")
	fmt.Println("- 标准的TCC模式实现")
	
	fmt.Println("缺点：")
	fmt.Println("- 数据冗余（状态既在分支表也在冻结表）")
	fmt.Println("- 维护复杂度增加")
	fmt.Println("- 存储开销更大")
}