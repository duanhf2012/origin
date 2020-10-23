package timewheel

import (
	"fmt"
	"testing"
	"time"
)

var timerCount int

var mapId map[int] interface{}

func Test_Example(t *testing.T) {
	now := time.Now()
	timer := NewTimer(time.Millisecond*20)
	select {
		case <-timer.C:
			fmt.Print("xxx:")
	}
	fmt.Println(time.Now().Sub(now).Milliseconds())
	/*
	rand.Seed(time.Now().UnixNano())
	timeWheel := NewTimeWheel()
	mapId = map[int] interface{}{}

	time.Sleep(time.Duration(rand.Intn(100))*time.Millisecond)
	timeWheel.Tick()
	time.AfterFunc()

	for i:=100000000;i<200000000;i++{
		r := rand.Intn(100)
		timeWheel.AddTimer(i+r,func(){
			fmt.Print("+\n")
		})

		time.NewTicker()
		time.AfterFunc()

		timerCount+=1
	}

	fmt.Println("add finish..")

	go func(){
		for{
			select {
			case t:=<-timeWheel.chanTimer:

				timerCount--
				if timerCount == 0 {
					fmt.Printf("finish...\n")
				}
				if t.tmp-t.expireTicks >1 {
					fmt.Printf("err:%d:%d\n",t.expireTicks,t.tmp-t.expireTicks)
				}else{

					if t.expireTicks%100000 == 0 {
						fmt.Printf("%d:%d:%d\n",t.expireTicks,t.tmp-t.expireTicks,t.tmpMilSeconds)
					}

					//t.timerCB()
				}
			}
		}
	}()

	for{

		timeWheel.TickOneFrame()
		//time.Sleep(1*time.Microsecond)
		//fmt.Println(".")
	}
*/
}
