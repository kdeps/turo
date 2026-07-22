//go:build ignore
package main
import "fmt"
func main() {
    fmt.Println("classify(over):", classify("over"))
    fmt.Println("dict size:", len(loadDict()))
}
