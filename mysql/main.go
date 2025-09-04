package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

//var (
//	db  *sql.DB
//	err error
//)

func init1() {
	// 连接到 MySQL 数据库
	dsn := "root:123456@tcp(127.0.0.1:3306)/dbname?parseTime=true&parseTime=true&loc=Asia%2FShanghai"
	db, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatal(err)
	}

	// 设置会话时区为 UTC
	_, err = db.Exec("SET time_zone = '+08:00'")
	//_, err = db.Exec("SET time_zone = 'UTC'")
	if err != nil {
		log.Fatal(err)
	}
	//defer db.Close()
}
func main1() {
	Insert()
	Raw()
}
func Insert() {
	// 获取当前时间
	now := time.Now()
	fmt.Println("Current Time (Local):", now)

	// 创建东四区时区
	//eastFour := time.FixedZone("UTC+4", 4*3600)

	// 获取当前东四区时间
	//now := time.Now().In(eastFour)
	//fmt.Println("Current Time (East UTC+4):", now)

	// 插入时间到数据库
	_, err = db.Exec("INSERT INTO your_table (timestamp_column, datetime_column) VALUES (?, ?)", now, now)
	if err != nil {
		log.Fatal(err)
	}

	// 从数据库中查询时间
	var timestampColumn time.Time
	var datetimeColumn time.Time
	err = db.QueryRow("SELECT timestamp_column, datetime_column FROM your_table ORDER BY id DESC LIMIT 1").Scan(&timestampColumn, &datetimeColumn)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Timestamp from DB (UTC):", timestampColumn)
	fmt.Println("Datetime from DB (Original):", datetimeColumn)

	// 将时间转换为本地时区
	//localTimestamp := timestampColumn.In(time.Local)
	//localDatetime := datetimeColumn.In(time.Local)
	//fmt.Println("Timestamp from DB (Local):", localTimestamp)
	//fmt.Println("Datetime from DB (Local):", localDatetime)
}

func Raw() {
	// 执行查询语句
	row := db.QueryRow("SELECT timestamp_column, datetime_column FROM your_table ORDER BY id DESC LIMIT 1")

	// 创建一个 map 来存储结果
	result := make(map[string]interface{})

	// 使用 []byte 来存储原生数据
	var timestampColumn, datetimeColumn []byte

	// 扫描结果到 []byte 类型的变量中
	err := row.Scan(&timestampColumn, &datetimeColumn)
	if err != nil {
		log.Fatal(err)
	}

	// 将原生数据存入 map 中
	result["timestamp_column"] = timestampColumn
	result["datetime_column"] = datetimeColumn

	// 输出 map 结果
	for key, value := range result {
		fmt.Printf("RAW: %s: %s\n", key, string(value.([]byte))) // 将 []byte 转换为字符串输出
	}

}
