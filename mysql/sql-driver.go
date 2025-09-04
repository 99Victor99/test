package main

import (
	"context"
	"database/sql"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func main4() {
	dsn := "root:123456@tcp(127.0.0.1:3306)/dbname"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 设置最大打开连接数
	db.SetMaxOpenConns(2000)

	// 设置最大空闲连接数
	db.SetMaxIdleConns(500)

	// 设置连接的最大生命周期
	db.SetConnMaxLifetime(30 * time.Minute)

	// 示例查询
	for i := 0; i < 200; i++ {
		go func(i int) {
			//ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			//defer cancel()
			ctx := context.Background()
			rows, err := db.QueryContext(ctx, "SELECT SLEEP(3)")

			if err != nil {
				log.Printf("Query %d failed: %v", i, err)
				return
			}
			rows.Close()
			log.Printf("Query %d succeeded", i)
		}(i)
	}

	// 等待所有查询完成
	time.Sleep(20 * time.Second)
}
