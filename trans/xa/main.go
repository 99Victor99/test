package main

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// XAContext 应用层事务上下文，用于在分支间传递数据
type XAContext struct {
	GlobalXID string
	UserID    int64
	UserName  string
	Age       int
	Detail    string
	Phone     string
	Address   string
	Points    int
	Email     string
}

// Branch XA分支定义
type Branch struct {
	ID   string
	DB   *sql.DB
	Name string
}

// XAManager 管理 XA 事务
type XAManager struct {
	branches  map[string]*Branch
	globalXID string
	mu        sync.RWMutex
	prepared  map[string]bool // 记录已准备的分支
}

// NewXAManager 初始化 XA 管理器
func NewXAManager(globalXID string) *XAManager {
	return &XAManager{
		branches:  make(map[string]*Branch),
		globalXID: globalXID,
		prepared:  make(map[string]bool),
	}
}

// AddBranch 添加XA分支
// "db1", "Database1", db1
// "db2", "Database2", db2
func (xm *XAManager) AddBranch(id, name string, db *sql.DB) {
	xm.mu.Lock()
	defer xm.mu.Unlock()
	xm.branches[id] = &Branch{
		ID:   id,
		DB:   db,
		Name: name,
	}
}

// StartXA 开始 XA 事务
func (xm *XAManager) StartXA(branchID string) error {
	xm.mu.RLock()
	branch, exists := xm.branches[branchID]
	xm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("branch %s not found", branchID)
	}

	xid := fmt.Sprintf("%s,%s", xm.globalXID, branchID)
	_, err := branch.DB.Exec(fmt.Sprintf("XA START '%s'", xid))
	if err != nil {
		return fmt.Errorf("XA START %s: %v", branchID, err)
	}
	return nil
}

// EndAndPrepare 结束并准备XA分支
func (xm *XAManager) EndAndPrepare(branchID string) error {
	xm.mu.RLock()
	branch, exists := xm.branches[branchID]
	xm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("branch %s not found", branchID)
	}

	xid := fmt.Sprintf("%s,%s", xm.globalXID, branchID)

	// XA END
	_, err := branch.DB.Exec(fmt.Sprintf("XA END '%s'", xid))
	if err != nil {
		return fmt.Errorf("XA END %s: %v", branchID, err)
	}

	// XA PREPARE
	_, err = branch.DB.Exec(fmt.Sprintf("XA PREPARE '%s'", xid))
	if err != nil {
		return fmt.Errorf("XA PREPARE %s: %v", branchID, err)
	}

	xm.mu.Lock()
	xm.prepared[branchID] = true
	xm.mu.Unlock()

	return nil
}

// CommitAll 提交所有已准备的分支
func (xm *XAManager) CommitAll() error {
	xm.mu.RLock()
	defer xm.mu.RUnlock()

	for branchID := range xm.prepared {
		branch := xm.branches[branchID]
		xid := fmt.Sprintf("%s,%s", xm.globalXID, branchID)
		_, err := branch.DB.Exec(fmt.Sprintf("XA COMMIT '%s'", xid))
		if err != nil {
			return fmt.Errorf("XA COMMIT %s: %v", branchID, err)
		}
	}
	return nil
}

// RollbackAll 回滚所有分支
func (xm *XAManager) RollbackAll() error {
	xm.mu.RLock()
	defer xm.mu.RUnlock()

	var lastErr error
	for branchID := range xm.branches {
		branch := xm.branches[branchID]
		xid := fmt.Sprintf("%s,%s", xm.globalXID, branchID)
		_, err := branch.DB.Exec(fmt.Sprintf("XA ROLLBACK '%s'", xid))
		if err != nil {
			lastErr = err
			log.Printf("XA ROLLBACK %s: %v", branchID, err)
		}
	}
	return lastErr
}

// RecoverXA 恢复未完成的XA事务
func (xm *XAManager) RecoverXA() error {
	xm.mu.RLock()
	defer xm.mu.RUnlock()

	for branchID, branch := range xm.branches {
		rows, err := branch.DB.Query("XA RECOVER")
		if err != nil {
			log.Printf("XA RECOVER failed for branch %s: %v", branchID, err)
			continue
		}

		for rows.Next() {
			var formatID, gtridLength, bqualLength int
			var data []byte
			err := rows.Scan(&formatID, &gtridLength, &bqualLength, &data)
			if err != nil {
				log.Printf("Scan XA RECOVER result failed: %v", err)
				continue
			}

			// 检查是否是我们的事务
			xid := string(data)
			if len(xid) > len(xm.globalXID) && xid[:len(xm.globalXID)] == xm.globalXID {
				log.Printf("Found unfinished XA transaction: %s, rolling back", xid)
				_, err := branch.DB.Exec(fmt.Sprintf("XA ROLLBACK '%s'", xid))
				if err != nil {
					log.Printf("XA ROLLBACK %s failed: %v", xid, err)
				}
			}
		}
		rows.Close()
	}
	return nil
}

// ExecuteUserOperations 执行用户相关操作
func (xm *XAManager) ExecuteUserOperations(ctx *XAContext) error {
	branchID := "db1"
	branch := xm.branches[branchID]

	// 插入用户
	result, err := branch.DB.Exec(
		"INSERT INTO user (name, age, detail, created_at) VALUES (?, ?, ?, ?)",
		ctx.UserName, ctx.Age, ctx.Detail, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("insert user: %v", err)
	}

	// 获取用户ID并存储到上下文
	userID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("get user_id: %v", err)
	}
	ctx.UserID = userID

	// 插入用户信息
	_, err = branch.DB.Exec(
		"INSERT INTO userinfo (user_id, phone, address, created_at) VALUES (?, ?, ?, ?)",
		ctx.UserID, ctx.Phone, ctx.Address, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("insert userinfo: %v", err)
	}

	return nil
}

// ExecuteScoreOperations 执行积分相关操作
func (xm *XAManager) ExecuteScoreOperations(ctx *XAContext) error {
	branchID := "db2"
	branch := xm.branches[branchID]

	// 插入积分
	_, err := branch.DB.Exec(
		"INSERT INTO score (user_id, points, created_at) VALUES (?, ?, ?)",
		ctx.UserID, ctx.Points, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("insert score: %v", err)
	}

	// 插入邮件
	_, err = branch.DB.Exec(
		"INSERT INTO email (user_id, email_content, created_at) VALUES (?, ?, ?)",
		ctx.UserID, ctx.Email, time.Now(),
	)
	if err != nil {
		return fmt.Errorf("insert email: %v", err)
	}

	return nil
}

// ExecuteXA 执行 XA 事务
func (xm *XAManager) ExecuteXA() error {
	// 创建事务上下文
	ctx := &XAContext{
		GlobalXID: xm.globalXID,
		UserName:  "Alice",
		Age:       25,
		Detail:    "Software engineer",
		Phone:     "1234567890",
		Address:   "123 Main St",
		Points:    100,
		Email:     "Welcome email",
	}

	// 启动所有XA分支
	for branchID := range xm.branches {
		if err := xm.StartXA(branchID); err != nil {
			xm.RollbackAll()
			return err
		}
	}

	// 执行db1操作
	if err := xm.ExecuteUserOperations(ctx); err != nil {
		xm.RollbackAll()
		return err
	}

	// 结束并准备db1分支
	if err := xm.EndAndPrepare("db1"); err != nil {
		xm.RollbackAll()
		return err
	}

	// 执行db2操作
	if err := xm.ExecuteScoreOperations(ctx); err != nil {
		xm.RollbackAll()
		return err
	}

	// 结束并准备db2分支
	if err := xm.EndAndPrepare("db2"); err != nil {
		xm.RollbackAll()
		return err
	}

	// 提交所有分支
	if err := xm.CommitAll(); err != nil {
		xm.RollbackAll()
		return err
	}

	return nil
}

func main() {
	// 连接两个 MySQL 实例
	db1, err := sql.Open("mysql", "root:123456@tcp(localhost:3306)/test_db?parseTime=true")
	if err != nil {
		log.Fatal(err)
	}
	defer db1.Close()

	db2, err := sql.Open("mysql", "root:123456@tcp(localhost:3307)/test_db?parseTime=true")
	if err != nil {
		log.Fatal(err)
	}
	defer db2.Close()

	// 创建 XA 管理器
	globalXID := "xa_tx_" + time.Now().Format("20060102150405")
	xm := NewXAManager(globalXID)

	// 添加分支
	xm.AddBranch("db1", "Database1", db1)
	xm.AddBranch("db2", "Database2", db2)

	// 恢复未完成的事务
	if err := xm.RecoverXA(); err != nil {
		log.Printf("XA recovery failed: %v", err)
	}

	// 执行 XA 事务
	if err := xm.ExecuteXA(); err != nil {
		log.Fatal("XA failed:", err)
	}
	fmt.Println("XA transaction completed successfully")
}
