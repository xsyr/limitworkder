package limitworker

import (
	"log"
    "fmt"
	"os"
	"strconv"
    "strings"
    "time"
	"sync"
	"syscall"
)

const (
    quitFlag = "5d659f81-f6c2-4229-82cf-66e01b7940cd"
)

// LimitWorker LimitWorker is used to dynamically increase or decrease the number of workers.
type LimitWorker interface {
	Dying() <-chan struct{}
	Kill()
	Wait()
	Close()
}

// Fn function to create a new worker.
type Fn func(int, <-chan struct{}) error

type limitWorker struct {
	log     *log.Logger
	logFile *os.File

    fn      Fn
	dying   chan struct{}
	done    chan struct{}
    id      int
	running int
	termErr int
	termOk  int
	lock    sync.Mutex

	cmder     string
	cmderQuit chan struct{}
}

// New create a LimitWorker instance,
// cmder a fifo file that is used to control the LimitWorker instance,
// $ echo "+1" > limit.cmder # increase a worker.
// $ echo "-1" > limit.cmder # decrease a worker.
// $ echo "*"  > limit.cmder # print current status.
// lf log file.
// fn function to create a new worker.
func New(num int, cmder, lf string, fn Fn) (LimitWorker, error) {
	logFile, err := os.OpenFile(lf, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
        return nil, fmt.Errorf("open log(%s) failed, err: %s", lf, err)
	}
	logger := log.New(logFile, "", log.LstdFlags|log.Lshortfile)

	err = syscall.Mkfifo(cmder, 0644)
	if err != nil && err != syscall.EEXIST {
		logFile.Close()
        return nil, fmt.Errorf("Mkfifo(%s) failed, err: %s", cmder, err)
	}

	lw := &limitWorker{
		log     : logger,
		logFile : logFile,

        fn      : fn,
		dying   : make(chan struct{}),
		done    : make(chan struct{}),
        id      : 0,
		running : 0,
		termErr : 0,
		termOk  : 0,
		lock    : sync.Mutex{},

		cmder     : cmder,
		cmderQuit : make(chan struct{}),
	}

	for i := 0; i < num; i++ {
		go lw.run(fn)
	}
    go lw.ctrl()

	return lw, nil
}

func (lw *limitWorker) run(fn Fn) {
	lw.lock.Lock()

    select {
    case <-lw.done:
        lw.lock.Unlock()
        return
    default:
    }
    lw.id++
    id := lw.id
	lw.running++
    lw.log.Printf("[%d]+ (running: %d, termErr: %d, termOk: %d)",
                  id, lw.running, lw.termErr, lw.termOk)

	lw.lock.Unlock()

	err := fn(id, lw.dying)

	lw.lock.Lock()

	lw.running--
	if err != nil {
		lw.termErr++
	} else {
		lw.termOk++
	}

	if err != nil {
		lw.log.Printf("[%d]- (running: %d, termErr: %d, termOk: %d), err: %s",
                      id, lw.running, lw.termErr, lw.termOk, err)
	} else {
		lw.log.Printf("[%d]- (running: %d, termErr: %d, termOk: %d)",
                      id, lw.running, lw.termErr, lw.termOk)
	}

	if lw.running == 0 {
        c, err := os.OpenFile(lw.cmder, os.O_WRONLY, 0644)
        if err != nil {
            lw.log.Printf("open cmder(%s) failed, err: %s", lw.cmder, err)
        } else {
            _, err := c.WriteString(quitFlag)
            if err != nil {
                lw.log.Printf("write data to cmder failed, err: %s", err)
            }
        }
        <-lw.cmderQuit
        close(lw.done)
	}

	lw.lock.Unlock()
}

func (lw *limitWorker) ctrl() {
    defer close(lw.cmderQuit)

    buf := make([]byte, 1024)
    for {
        c, err := os.OpenFile(lw.cmder, os.O_RDONLY, 0644)
        if err != nil {
            lw.log.Printf("open cmder(%s) failed, err: %s", lw.cmder, err)
            continue
        }

        n, err := c.Read(buf)
        if err != nil {
            lw.log.Printf("read data from cmder failed, err: %s", err)
            continue
        }

        cmd := strings.Trim(string(buf[:n]), "\r\n")
        if cmd[0] == '*' {
            lw.log.Printf("running: %d, termErr: %d, termOk: %d",
                          lw.running, lw.termErr, lw.termOk)
            continue
        }

        if cmd == quitFlag { break }

        delta, err := strconv.Atoi(cmd)
        if err != nil {
            lw.log.Printf("unknown command '%s', err: %s", cmd, err)
            continue
        }

        if delta > 0 {
            if delta > 5 { delta = 5 }
            for i := 0; i < delta; i++ {
                go lw.run(lw.fn)
            }
        } else if delta < 0 {
            if delta < -5 { delta = -5}
            tick := time.Tick(50 * time.Millisecond)
            for i := 0; i > delta; i-- {
                for timeout := true; timeout; {
                    select {
                    case lw.dying <-struct{}{}: timeout = false
                    case <-tick: timeout = true
                    }
                }
            }
        }

        lw.log.Printf("delta: %+d", delta)
    }

    lw.log.Println("cmder quit now ...")
}

func (lw *limitWorker) Dying() <-chan struct{} {
	return lw.dying
}

func (lw *limitWorker) Kill() {
    lw.lock.Lock()
    select {
    case <-lw.done:
    case <-lw.dying:
    default: close(lw.dying)
    }
    lw.lock.Unlock()
}

func (lw *limitWorker) Wait() {
	<-lw.done
}

func (lw *limitWorker) Close() {
    lw.Kill()
	lw.logFile.Close()
}

func fdSET(p *syscall.FdSet, i int) {
    p.Bits[i/64] |= 1 << uint(i) % 64
}

func fdZERO(p *syscall.FdSet) {
    for i := range p.Bits {
        p.Bits[i] = 0
    }
}


