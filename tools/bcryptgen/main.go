package main

import (
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"
)

// bcryptgen 生成 bcrypt 哈希（用于运维直接更新密码场景），并自校验。
// 用法：
//
//	bcryptgen <password>                 生成哈希
//	bcryptgen -verify <hash> <password>  校验哈希
func main() {
	if len(os.Args) >= 4 && os.Args[1] == "-verify" {
		if err := bcrypt.CompareHashAndPassword([]byte(os.Args[2]), []byte(os.Args[3])); err != nil {
			fmt.Fprintln(os.Stderr, "MISMATCH")
			os.Exit(1)
		}
		fmt.Println("MATCH")
		return
	}
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bcryptgen <password>  |  bcryptgen -verify <hash> <password>")
		os.Exit(1)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(os.Args[1]), bcrypt.DefaultCost)
	if err != nil {
		panic(err)
	}
	if err := bcrypt.CompareHashAndPassword(h, []byte(os.Args[1])); err != nil {
		panic("self-check failed")
	}
	fmt.Print(string(h))
}
