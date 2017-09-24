
# demo

```go
package main

import (
	"fmt"
  "time"

	"github.com/xsyr/limitworker"
)

func foo(id int, dying <-chan struct{}) error {
    i := 0
    for {
        quit := false
        select {
        case <-dying: quit = true
        default:
        }

        if quit { break }

        fmt.Printf("[%d] foo\n", id)
        time.Sleep(1 * time.Second)

        i++
        if i == 20 { break }
    }

    fmt.Printf("[%d] foo quit\n", id)
	return nil
}

func main() {
	lw, err := limitworker.New(2, "ctrl", "ctrl.log", foo)
	if err != nil {
		fmt.Println(err)
		return
	}

	lw.Wait()
	lw.Close()
}
```

# increase two workers
```sh
echo '+2' > ctrl
```
`ctrl.log` log the details:
```
2017/09/24 10:53:43 limitworker.go:206: delta: +2
2017/09/24 10:53:43 limitworker.go:106: [3]+ (running: 3, termErr: 0, termOk: 0)
2017/09/24 10:53:43 limitworker.go:106: [4]+ (running: 4, termErr: 0, termOk: 0)
```

# decrease three workders
```sh
echo '-3' > ctrl
```
`ctrl.log` log the details:
```
2017/09/24 10:53:46 limitworker.go:126: [3]- (running: 3, termErr: 0, termOk: 1)
2017/09/24 10:53:46 limitworker.go:126: [1]- (running: 2, termErr: 0, termOk: 2)
2017/09/24 10:53:46 limitworker.go:126: [2]- (running: 1, termErr: 0, termOk: 3)
2017/09/24 10:53:46 limitworker.go:206: delta: -3
```
