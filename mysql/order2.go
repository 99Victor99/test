package main

import (
	"database/sql"
	"fmt"
	"github.com/google/uuid"
	"log"
	"math/rand"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func main3() {
	// 连接到 MySQL 数据库
	dsn := "root:123456@tcp(127.0.0.1:3306)/dbname?parseTime=true&parseTime=true&loc=Asia%2FShanghai"
	db, err := sql.Open("mysql", dsn)
	db.SetConnMaxLifetime(time.Hour * 4) // 允许连接存活的最大时间
	db.SetMaxOpenConns(20)               // 最大打开连接数
	db.SetMaxIdleConns(10)               // 最大空闲连接数

	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	// 确保连接有效
	err = db.Ping()
	if err != nil {
		log.Fatal(err)
	}

	// 准备批量插入
	batchSize := 5000        // 每批次插入的数据量
	totalRecords := 20000000 // 需要插入的总数据量
	for i := 0; i < totalRecords/batchSize; i++ {
		//time.Sleep(100 * time.Millisecond)
		// 构建批量插入的 SQL 语句
		sqlStr := "INSERT INTO order3s (order_number, customer_id, order_date, status, total_amount, shipping_address, shipping_cost, payment_method, discount_code, tax_amount, items_count, delivery_date, notes) VALUES "
		vals := []interface{}{}

		for j := 0; j < batchSize; j++ {
			orderNumber := uuid.New()
			customerID := rand.Int63n(1000000)
			orderDate := time.Now().AddDate(0, 0, -rand.Intn(1000)).Format("2006-01-02 15:04:05")
			status := "PENDING"
			totalAmount := rand.Float64() * 1000
			shippingAddress := "123 Some St, Some City"
			shippingCost := rand.Float64() * 20
			paymentMethod := "Credit Card"
			discountCode := "DISCOUNT2024"
			taxAmount := totalAmount * 0.1
			itemsCount := rand.Intn(10)
			deliveryDate := time.Now().AddDate(0, 0, rand.Intn(30)).Format("2006-01-02 15:04:05")
			notes := "Some notes about the order"

			sqlStr += "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),"
			vals = append(vals, orderNumber, customerID, orderDate, status, totalAmount, shippingAddress, shippingCost, paymentMethod, discountCode, taxAmount, itemsCount, deliveryDate, notes)
		}

		// 去掉最后的逗号
		sqlStr = sqlStr[0 : len(sqlStr)-1]

		// 执行批量插入
		stmt, err := db.Prepare(sqlStr)
		if err != nil {
			log.Fatal(err)
		}

		_, err = stmt.Exec(vals...)
		if err != nil {
			log.Fatal(err)
		}
		stmt.Close()

		fmt.Printf("Inserted batch %d\n", i+1)
	}

	fmt.Println("All records inserted successfully!")
}
