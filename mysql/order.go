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

// Order structure for storing the order data
//
//	 `order_id` bigint NOT NULL AUTO_INCREMENT,
//	`order_number` varchar(50) COLLATE utf8mb4_bin NOT NULL,
//	`customer_id` bigint NOT NULL,
//	`order_date` datetime NOT NULL,
//	`status` varchar(20) COLLATE utf8mb4_bin NOT NULL,
//	`total_amount` decimal(10,2) NOT NULL,
//	`shipping_address` text COLLATE utf8mb4_bin,
//	`shipping_cost` decimal(10,2) DEFAULT NULL,
//	`payment_method` varchar(20) COLLATE utf8mb4_bin DEFAULT NULL,
//	`discount_code` varchar(50) COLLATE utf8mb4_bin DEFAULT NULL,
//	`tax_amount` decimal(10,2) DEFAULT NULL,
//	`items_count` int DEFAULT NULL,
//	`delivery_date` datetime DEFAULT NULL,
//	`notes` text COLLATE utf8mb4_bin,
//	`created_at` timestamp NULL DEFAULT CURRENT_TIMESTAMP,
type Order struct {
	OrderNumber     string
	CustomerID      int64
	OrderDate       time.Time
	Status          string
	TotalAmount     float64
	ShippingAddress string
	ShippingCost    float64
	PaymentMethod   string
	DiscountCode    string
	TaxAmount       float64
	ItemsCount      int
	DeliveryDate    time.Time
	Notes           string
	CreatedAt       time.Time
}

// GenerateRandomOrder generates a random order
func GenerateRandomOrder() Order {
	return Order{
		OrderNumber:     uuid.New().String(),
		CustomerID:      rand.Int63n(1000000), // 假设有 100 万客户
		OrderDate:       time.Now(),
		Status:          "PENDING",
		TotalAmount:     rand.Float64() * 1000, // 随机订单金额，最大 1000
		ShippingAddress: "123 Some St, Some City",
		ShippingCost:    rand.Float64() * 20, // 随机运费，最大 20
		PaymentMethod:   "Credit Card",
		DiscountCode:    "DISCOUNT2024",
		TaxAmount:       0.1,           // 税率 10%
		ItemsCount:      rand.Intn(10), // 随机商品数量，最大 10
		DeliveryDate:    time.Now().AddDate(0, 0, rand.Intn(30)),
		Notes:           "Some notes about the order",
		CreatedAt:       time.Now(),
	}
}

// InsertOrdersInBatch inserts orders in batch
func InsertOrdersInBatch(db *sql.DB, orders []Order) error {
	query := "INSERT INTO order2s (order_number, customer_id, order_date, status, total_amount, shipping_address, shipping_cost, payment_method, discount_code, tax_amount, items_count, delivery_date, notes) VALUES "
	values := ""

	// Create the values string with placeholders for batch insert
	for _, order := range orders {
		values += fmt.Sprintf("('%s', %d, '%s', '%s', %.2f, '%s', %.2f, '%s', '%s', %.2f, %d, '%s', '%s'),",
			order.OrderNumber, order.CustomerID, order.OrderDate.Format("2006-01-02 15:04:05"), order.Status, order.TotalAmount, order.ShippingAddress,
			order.ShippingCost, order.PaymentMethod, order.DiscountCode, order.TaxAmount, order.ItemsCount, order.DeliveryDate.Format("2006-01-02 15:04:05"), order.Notes)

		//values += fmt.Sprintf("('%s', %d, '%s', '%s', %.2f),",
		//	order.OrderNumber, order.CustomerID, order.OrderDate.Format("2006-01-02 15:04:05"), order.Status, order.TotalAmount)
	}

	// Remove the last comma and append to the query
	query += values[:len(values)-1]

	// Execute the batch insert query
	_, err := db.Exec(query)
	return err
}

func main5() {
	// 连接 MySQL 数据库
	dsn := "root:123456@tcp(127.0.0.1:3306)/dbname?parseTime=true&parseTime=true&loc=Asia%2FShanghai"
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal("Failed to connect to database:", err)
	}
	defer db.Close()

	// 确认数据库连接有效
	err = db.Ping()
	if err != nil {
		log.Fatal("Failed to ping database:", err)
	}

	// 每次插入1000条数据，持续插入1千万条记录
	batchSize := 5000
	totalOrders := 10000000 * 2
	var orders []Order

	for i := 0; i < totalOrders; i++ {
		orders = append(orders, GenerateRandomOrder())

		// 批量插入
		if len(orders) == batchSize {
			err := InsertOrdersInBatch(db, orders)
			if err != nil {
				log.Fatal("Failed to insert batch:", err)
			}

			// 清空当前批次
			orders = nil
		}
		fmt.Printf("Inserted batch %d\n", i+1)
	}

	// 如果还有剩余未插入的数据
	if len(orders) > 0 {
		err := InsertOrdersInBatch(db, orders)
		if err != nil {
			log.Fatal("Failed to insert remaining orders:", err)
		}
	}

	fmt.Println("Inserted all orders successfully!")
}
