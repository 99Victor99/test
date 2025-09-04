package main

import (
	"fmt"
	"time"
)

func main() {
	// 错误的用法示例
	nsec := time.Now().UnixNano()
	fmt.Println("总纳秒数:", nsec)

	// 错误：直接将总纳秒数作为第二个参数
	// t := time.Unix(0, nsec)

	// 正确的用法1：分离秒和纳秒
	now := time.Now()
	sec := now.Unix()
	nanosec := now.Nanosecond()
	t1 := time.Unix(sec, int64(nanosec))
	fmt.Println("方法1 - 分离秒和纳秒:", t1)

	// 正确的用法2：从总纳秒数计算
	t2 := time.Unix(nsec/1e9, nsec%1e9)
	fmt.Println("方法2 - 从总纳秒数计算:", t2)

	// 正确的用法3：最简单的方法
	t3 := time.Now()
	fmt.Println("方法3 - 直接使用Now():", t3)

}
