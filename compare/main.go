package main

import (
	"fmt"
	"github.com/emirpasic/gods/queues/priorityqueue"
	"github.com/emirpasic/gods/utils"
)

func main() {
	// 创建一个优先级队列，使用 utils.IntComparator 比较器
	queue := priorityqueue.NewWith(utils.IntComparator) // 创建一个整型优先级队列

	// 入队元素
	queue.Enqueue(3)  // 插入3
	queue.Enqueue(1)  // 插入1
	queue.Enqueue(2)  // 插入2
	queue.Enqueue(10) // 插入2
	queue.Enqueue(9)  // 插入2

	// 获取队列中的最小值（因为使用的比较器是升序）
	value, _ := queue.Peek()
	fmt.Println("Peek:", value) // 输出: Peek: 1
	value, _ = queue.Peek()
	fmt.Println("Peek:", value) // 输出: Peek: 1
	value, _ = queue.Peek()
	fmt.Println("Peek:", value) // 输出: Peek: 1

	fmt.Println("----------------") // 输出: Peek: 1

	// 出队元素（按优先级出队）
	value, _ = queue.Dequeue()
	fmt.Println("Dequeue:", value) // 输出: Dequeue: 1
	value, _ = queue.Dequeue()
	fmt.Println("Dequeue:", value) // 输出: Dequeue: 1
	value, _ = queue.Dequeue()
	fmt.Println("Dequeue:", value) // 输出: Dequeue: 1

	// 获取队列长度
	fmt.Println("Size:", queue.Size()) // 输出: Size: 2
}
