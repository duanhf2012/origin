package timewheel

import (
	"testing"
	"time"
	"fmt"
)

func Test_Example(t *testing.T) {
	timer:=NewTimer(time.Second*2)
	select {
		case <- timer.C:
			fmt.Println("It is time out!")
	}

	timer2 := NewTimerEx(time.Second*2,nil,1)
	select {
	case t:=<- timer2.C:
		fmt.Println("It is time out!",t.AdditionData.(int))
	}

	timer3 := NewTimerEx(time.Second*2,nil,1)
	timer3.Stop()
	time.Sleep(3*time.Second)
	select {
	case t:=<- timer2.C:
		fmt.Println("It is time out!",t.AdditionData.(int))
	default:
		fmt.Printf("time is stop")
	}
}
