package main

import (
	"database/sql"
	"fmt"
	"log"
	"math/rand"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var (
	tableName = "worker_quality_sessions"
	db        *sql.DB
	err       error
)

func init() {
	// 连接到 MySQL 数据库
	dsn := "root:123456@tcp(127.0.0.1:3306)/wcs_core?parseTime=true&parseTime=true&loc=Asia%2FShanghai"
	db, err = sql.Open("mysql", dsn)
	db.SetConnMaxLifetime(time.Hour * 4) // 允许连接存活的最大时间
	db.SetMaxOpenConns(20)               // 最大打开连接数
	db.SetMaxIdleConns(10)               // 最大空闲连接数

	if err != nil {
		log.Fatal(err)
	}

	// 确保连接有效
	err = db.Ping()
	if err != nil {
		log.Fatal(err)
	}
}

func CreateTable() {
	query := `CREATE TABLE IF NOT EXISTS ` + tableName + ` (
  id bigint NOT NULL AUTO_INCREMENT,
  binding_session_id bigint NOT NULL DEFAULT '0' COMMENT '绑定会话记录id->worker_binding_relationship_log.id',
  tenant_id smallint NOT NULL DEFAULT '0' COMMENT '商户id',
  consult_id tinyint unsigned NOT NULL DEFAULT '0' COMMENT '咨询类型id',
  worker_id smallint NOT NULL DEFAULT '0' COMMENT '接待客服id',
  uid int NOT NULL DEFAULT '0' COMMENT '客户id',
  user_role tinyint NOT NULL DEFAULT '0' COMMENT '客户角色',
  user_level tinyint NOT NULL DEFAULT '0' COMMENT '用户层级',
  check_type tinyint unsigned NOT NULL DEFAULT '0' COMMENT '@enum(wcs/api/common/WorkerCheckType) 质检类型 0-普通 1-必检 2-联检',
  first_send_time int unsigned NOT NULL DEFAULT '0' COMMENT '首次发送消息时间',
  last_reply_time int unsigned NOT NULL DEFAULT '0' COMMENT '最后回复消息时间',
  last_end_time int unsigned NOT NULL DEFAULT '0' COMMENT '最后消息结束时间',
  service_duration int unsigned NOT NULL DEFAULT '0' COMMENT '服务时长(s)',
  client_send_message_count int NOT NULL DEFAULT '0' COMMENT '客户发送消息计数',
  worker_send_message_count int NOT NULL DEFAULT '0' COMMENT '客服发送消息计数',
  read_duration int unsigned NOT NULL DEFAULT '0' COMMENT '质检时长(s)',
  score_worker_id smallint NOT NULL DEFAULT '0' COMMENT '质检客服id',
  score_type tinyint unsigned NOT NULL DEFAULT '0' COMMENT '@enum(wcs/api/common/WorkerScoreType)质检评级 1/2/3/4 优异/正常/较差/极差',
  score_time int unsigned NOT NULL DEFAULT '0' COMMENT '质检时间',
  review_worker_id smallint NOT NULL DEFAULT '0' COMMENT '复审客服id',
  review_score_type tinyint unsigned NOT NULL DEFAULT '0' COMMENT '@enum(wcs/api/common/WorkerScoreType)复审评级',
  review_time int unsigned NOT NULL DEFAULT '0' COMMENT '复审时间',
  created_at timestamp NOT NULL DEFAULT '1970-01-01 08:00:01' COMMENT '质检会话推送时间',
  group_max_score_time int unsigned NOT NULL DEFAULT '0' COMMENT '分组最大质检时间',
  PRIMARY KEY (id),
  KEY idx_tenant_id_worker_id (tenant_id,worker_id),
  KEY idx_score_worker_id (score_worker_id),
  KEY idx_created_at (created_at),
  KEY idx_binding_session_id (binding_session_id)
) ENGINE=InnoDB AUTO_INCREMENT=3369 DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_bin COMMENT='推送的客服质检会话表';`

	_, err = db.Exec(query)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	//defer db.Close()
	CreateTable()

	// 批量插入 500 万条数据，分批处理
	batchSize := 2000 // 每次插入 1000 条记录
	totalRows := 5000000

	for i := 0; i < totalRows/batchSize; i++ {
		query := "INSERT INTO " + tableName + " (binding_session_id, tenant_id, consult_id, worker_id, uid, user_role, user_level, check_type, first_send_time, last_reply_time, last_end_time, service_duration, client_send_message_count, worker_send_message_count, read_duration, score_worker_id, score_type, score_time, review_worker_id, review_score_type, review_time, created_at, group_max_score_time) VALUES "

		params := make([]interface{}, 0, batchSize*23)

		for j := 0; j < batchSize; j++ {
			query += "(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?),"

			params = append(params,
				rand.Int63n(1000000),                     // binding_session_id
				232,                                      // tenant_id
				rand.Intn(100),                           // consult_id
				rand.Intn(227),                           // worker_id
				rand.Intn(100000),                        // uid
				2,                                        // user_role
				rand.Intn(10),                            // user_level
				rand.Intn(2),                             // check_type
				rand.Intn(2147483647),                    // first_send_time
				rand.Intn(2147483647),                    // last_reply_time
				rand.Intn(2147483647),                    // last_end_time
				rand.Intn(3600),                          // service_duratio
				rand.Intn(100),                           // client_send_message_count
				rand.Intn(100),                           // worker_send_message_count
				rand.Intn(3600),                          // read_duration
				rand.Intn(227),                           // score_worker_id
				rand.Intn(5),                             // score_type
				rand.Intn(2147483647),                    // score_time
				rand.Intn(1000),                          // review_worker_id
				rand.Intn(5),                             // review_score_type
				rand.Intn(2147483647),                    // review_time
				time.Now().Format("2006-01-02 15:04:05"), // created_at
				rand.Intn(2147483647))                    // group_max_score_time
		}

		query = query[:len(query)-1] // 移除最后的逗号

		// 执行插入
		_, err = db.Exec(query, params...)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("Batch %d inserted\n", i+1)
	}

}
