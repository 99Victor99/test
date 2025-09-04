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

type ResourceManager interface {
	Try(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error
	Confirm(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error
	Cancel(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error
}

type InventoryRM struct{}

func (rm *InventoryRM) Try(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	itemID := args["item_id"].(int)
	quantity := args["quantity"].(int)
	// 幂等: 检查version
	var version int
	err := tx.QueryRow("SELECT version FROM seckill_inventory WHERE item_id = ? FOR UPDATE", itemID).Scan(&version)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE seckill_inventory SET frozen = frozen + ?, available = available - ?, version = version + 1 WHERE item_id = ? AND version = ?", quantity, quantity, itemID, version)
	if err != nil {
		return err
	}
	return nil
}

func (rm *InventoryRM) Confirm(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	itemID := args["item_id"].(int)
	quantity := args["quantity"].(int)
	var version int
	err := tx.QueryRow("SELECT version FROM seckill_inventory WHERE item_id = ? FOR UPDATE", itemID).Scan(&version)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE seckill_inventory SET frozen = frozen - ?, total = total - ?, version = version + 1 WHERE item_id = ? AND version = ?", quantity, quantity, itemID, version)
	if err != nil {
		return err
	}
	return nil
}

func (rm *InventoryRM) Cancel(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	itemID := args["item_id"].(int)
	quantity := args["quantity"].(int)
	var version int
	err := tx.QueryRow("SELECT version FROM seckill_inventory WHERE item_id = ? FOR UPDATE", itemID).Scan(&version)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE seckill_inventory SET frozen = frozen - ?, available = available + ?, version = version + 1 WHERE item_id = ? AND version = ?", quantity, quantity, itemID, version)
	if err != nil {
		return err
	}
	return nil
}

// 类似地实现AccountRM和OrderRM（省略，逻辑类似，添加version幂等）
type AccountRM struct{}

// ... (实现Try, Confirm, Cancel with version check)
func (rm *AccountRM) Try(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	accountID := args["account_id"].(int)
	amount := args["amount"].(int)
	// 幂等: 检查version
	var version int
	err := tx.QueryRow("SELECT version FROM account WHERE account_id = ? FOR UPDATE", accountID).Scan(&version)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE account SET balance = balance - ?, version = version + 1 WHERE account_id = ? AND version = ?", amount, accountID, version)
	if err != nil {
		return err
	}
	return nil
}

func (rm *AccountRM) Confirm(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	accountID := args["account_id"].(int)
	amount := args["amount"].(int)
	var version int
	err := tx.QueryRow("SELECT version FROM account WHERE account_id = ? FOR UPDATE", accountID).Scan(&version)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE account SET balance = balance + ?, version = version + 1 WHERE account_id = ? AND version = ?", amount, accountID, version)
	if err != nil {
		return err
	}
	return nil
}

func (rm *AccountRM) Cancel(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	accountID := args["account_id"].(int)
	amount := args["amount"].(int)
	var version int
	err := tx.QueryRow("SELECT version FROM account WHERE account_id = ? FOR UPDATE", accountID).Scan(&version)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE account SET balance = balance + ?, version = version + 1 WHERE account_id = ? AND version = ?", amount, accountID, version)
	if err != nil {
		return err
	}
	return nil
}

type OrderRM struct{}

// ... (实现Try: INSERT with 'TRYING', Confirm: UPDATE to 'CONFIRMED', Cancel: UPDATE to 'CANCELLED' with version)
func (rm *OrderRM) Try(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	accountID := args["account_id"].(int)
	itemID := args["item_id"].(int)
	quantity := args["quantity"].(int)
	price := args["price"].(int)
	// 幂等: 检查version
	var version int
	err := tx.QueryRow("SELECT version FROM account WHERE account_id = ? FOR UPDATE", accountID).Scan(&version)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO seckill_order(account_id, item_id, quantity, price, version) VALUES(?, ?, ?, ?, ?)", accountID, itemID, quantity, price, version)
	if err != nil {
		return err
	}
	return nil
}

func (rm *OrderRM) Confirm(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	orderID := args["order_id"].(int)
	var version int
	err := tx.QueryRow("SELECT version FROM seckill_order WHERE order_id = ? FOR UPDATE", orderID).Scan(&version)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE seckill_order SET status = 'CONFIRMED', version = version + 1 WHERE order_id = ? AND version = ?", orderID, version)
	if err != nil {
		return err
	}
	return nil
}

func (rm *OrderRM) Cancel(ctx context.Context, tx *sql.Tx, args map[string]interface{}) error {
	orderID := args["order_id"].(int)
	var version int
	err := tx.QueryRow("SELECT version FROM seckill_order WHERE order_id = ? FOR UPDATE", orderID).Scan(&version)
	if err != nil {
		return err
	}
	_, err = tx.Exec("UPDATE seckill_order SET status = 'CANCELLED', version = version + 1 WHERE order_id = ? AND version = ?", orderID, version)
	if err != nil {
		return err
	}
	return nil
}

type Coordinator struct {
	db        *sql.DB
	resources map[string]ResourceManager
}

func NewCoordinator(db *sql.DB) *Coordinator {
	return &Coordinator{
		db: db,
		resources: map[string]ResourceManager{
			"inventory": &InventoryRM{},
			"account":   &AccountRM{},
			"order":     &OrderRM{},
		},
	}
}

func (c *Coordinator) StartTransaction(ctx context.Context, txID string, args map[string]interface{}) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Try阶段
	_, err = tx.Exec("INSERT INTO tcc_transaction(tx_id, status, create_time) VALUES(?, 'TRYING', NOW())", txID)
	if err != nil {
		tx.Rollback()
		return err
	}
	for resourceID, rm := range c.resources {
		if err := rm.Try(ctx, tx, args); err != nil {
			tx.Rollback()
			return err
		}
		branchID := uuid.New().String()
		_, err = tx.Exec("INSERT INTO tcc_branch(branch_id, tx_id, resource_id, status) VALUES(?, ?, ?, 'PREPARED')", branchID, txID, resourceID)
		if err != nil {
			tx.Rollback()
			return err
		}
	}
	_, err = tx.Exec("UPDATE tcc_transaction SET status = 'TRIED' WHERE tx_id = ? AND status = 'TRYING'", txID) // 幂等
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *Coordinator) Confirm(ctx context.Context, txID string, args map[string]interface{}) error {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// 幂等: 检查并更新到CONFIRMING
	res, err := tx.Exec("UPDATE tcc_transaction SET status = 'CONFIRMING' WHERE tx_id = ? AND status = 'TRIED'", txID)
	rows, _ := res.RowsAffected()
	if err != nil || rows == 0 {
		tx.Rollback()
		return fmt.Errorf("invalid state for confirm")
	}
	for _, rm := range c.resources {
		if err := rm.Confirm(ctx, tx, args); err != nil {
			tx.Rollback()
			return err
		}
	}
	_, err = tx.Exec("UPDATE tcc_branch SET status = 'CONFIRMED' WHERE tx_id = ?", txID)
	if err != nil {
		tx.Rollback()
		return err
	}
	_, err = tx.Exec("UPDATE tcc_transaction SET status = 'CONFIRMED' WHERE tx_id = ? AND status = 'CONFIRMING'", txID)
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (c *Coordinator) Cancel(ctx context.Context, txID string, args map[string]interface{}) error {
	// 类似Confirm，实现CANCELLING检查和更新（省略）
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// 幂等: 检查并更新到CANCELLING
	res, err := tx.Exec("UPDATE tcc_transaction SET status = 'CANCELLING' WHERE tx_id = ? AND status = 'TRIED'", txID)
	rows, err := res.RowsAffected()
	if err != nil || rows == 0 {
		tx.Rollback()
		return fmt.Errorf("invalid state for cancel")
	}
	for _, rm := range c.resources {
		if err := rm.Cancel(ctx, tx, args); err != nil {
			tx.Rollback()
			return err
		}
	}
	_, err = tx.Exec("UPDATE tcc_branch SET status = 'CANCELLED' WHERE tx_id = ?", txID)
	if err != nil {
		tx.Rollback()
		return err
	}
	_, err = tx.Exec("UPDATE tcc_transaction SET status = 'CANCELLED' WHERE tx_id = ? AND status = 'CANCELLING'", txID)
	if err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// 重启补偿: 定时扫描未完成事务
func (c *Coordinator) Compensate() {
	for {
		time.Sleep(1 * time.Minute)
		rows, err := c.db.Query("SELECT tx_id, status FROM tcc_transaction WHERE status IN ('TRYING', 'TRIED', 'CONFIRMING', 'CANCELLING') AND create_time < NOW() - INTERVAL 5 MINUTE")
		if err != nil {
			log.Println("compensate error:", err)
			continue
		}
		for rows.Next() {
			var txID, status string
			rows.Scan(&txID, &status)
			// 恢复args（示例：从分支表或其他快照恢复；实际需根据业务实现）
			args, err := c.recoverArgs(txID)
			if err != nil {
				log.Println("recover args failed for tx:", txID, err)
				continue
			}
			switch status {
			case "TRYING":
				// 所有资源未预留完毕，回滚
				if err := c.Cancel(context.Background(), txID, args); err != nil {
					log.Println("compensate TRYING failed:", err)
				}
			case "TRIED":
				// 所有资源已预留完毕，提交
				if err := c.Confirm(context.Background(), txID, args); err != nil {
					log.Println("compensate TRIED failed:", err)
				}
			case "CONFIRMING":
				// 资源扣减提交中，继续提交
				if err := c.Confirm(context.Background(), txID, args); err != nil {
					log.Println("compensate CONFIRMING failed:", err)
				}
			case "CANCELLING":
				// 资源回滚中，继续回滚
				if err := c.Cancel(context.Background(), txID, args); err != nil {
					log.Println("compensate CANCELLING failed:", err)
				}
			}
		}
		rows.Close()
	}
}

// 示例：恢复args的辅助函数（需根据实际存储实现）
func (c *Coordinator) recoverArgs(txID string) (map[string]interface{}, error) {
	// TODO: 从tcc_branch或其他表查询并重建args
	// 示例返回假数据；实际中查询数据库
	return map[string]interface{}{"item_id": 1, "quantity": 1, "user_id": 1, "order_id": "example", "amount": 100.0}, nil
}

func main() {
	db, err := sql.Open("mysql", "user:pass@tcp(localhost:3306)/db")
	if err != nil {
		log.Fatal(err)
	}
	c := NewCoordinator(db)
	go c.Compensate() // 启动补偿

	txID := uuid.New().String()
	args := map[string]interface{}{"item_id": 1, "quantity": 1, "user_id": 1, "order_id": uuid.New().String(), "amount": 100.0}
	err = c.StartTransaction(context.Background(), txID, args)
	if err != nil {
		log.Println("Try failed:", err)
		c.Cancel(context.Background(), txID, args)
		return
	}
	// 模拟业务成功
	err = c.Confirm(context.Background(), txID, args)
	if err != nil {
		log.Println("Confirm failed:", err)
		c.Cancel(context.Background(), txID, args)
	}
	fmt.Println("Transaction completed")
}
