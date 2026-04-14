package main

import (
	"fmt"
	"os"
)

var version = "dev"

func main() {
	fmt.Printf("taroctl version %s\n", version)
	fmt.Println("taro CLI tool")
	os.Exit(0)
}
