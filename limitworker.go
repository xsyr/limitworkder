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

	cmder     *os.File
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
        return nil, fmt.Errorf("open log file(%s) failed, err: %s", lf, err)
	}
	logger := log.New(logFile, "", log.LstdFlags|log.Lshortfile)

	err = syscall.Mkfifo(cmder, 0644)
	if err != nil && err != syscall.EEXIST {
		logFile.Close()
        return nil, fmt.Errorf("Mkfifo(%s) failed, err: %s", cmder, err)
	}

    c, err := os.OpenFile(cmder, os.O_RDWR | syscall.O_NONBLOCK, 0644)
    if err != nil {
		logFile.Close()
        return nil, fmt.Errorf("open cmder file(%s) failed, err: %s",
                               cmder, err)
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

		cmder     : c,
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
        lw.cmderQuit<-struct{}{}
        <-lw.cmderQuit
        close(lw.done)
	}

	lw.lock.Unlock()
}

func (lw *limitWorker) ctrl() {
    defer close(lw.cmderQuit)

    fd := int(lw.cmder.Fd())
    rfds := &syscall.FdSet{}
    buf := make([]byte, 1024)
    for {
        fdZERO(rfds)
        fdSET(rfds, fd)

        timeout := syscall.Timeval{ Sec: 0, Usec: 20 * 1000 }
        n, err := syscall.Select(fd + 1, rfds, nil, nil, &timeout)
        if err != nil {
            lw.log.Printf("syscall.Select() failed, err: %s", err)
            continue
        }

        quit := false
        select {
        case <-lw.cmderQuit: quit = true
        default:
        }
        if quit {
            lw.log.Println("job finish, cmder quit now ...")
            break
        }

        // timeout
        if n == 0 { continue }

        n, err = lw.cmder.Read(buf)
        if err != nil {
            lw.log.Printf("read data from cmder failed, err: %s. cmder quit now ...", err)
            break
        }

        cmd := strings.Trim(string(buf[:n]), "\r\n")
        if cmd[0] == '*' {
            lw.log.Printf("running: %d, termErr: %d, termOk: %d",
                          lw.running, lw.termErr, lw.termOk)
            continue
        }

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
    lw.cmder.Close()
}

func fdSET(p *syscall.FdSet, i int) {
    p.Bits[i/64] |= 1 << uint(i) % 64
}

func fdZERO(p *syscall.FdSet) {
    for i := range p.Bits {
        p.Bits[i] = 0
    }
}


