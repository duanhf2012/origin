package sysservice

import (
	"encoding/json"
	"fmt"
	"runtime/pprof"

	"github.com/duanhf2012/origin/service"
)

type PProfService struct {
	service.BaseService
}

type ProfileData struct {
	Name  string
	Count int
}

type Profilestruct struct {
	ProfileList []ProfileData
}

func (slf *PProfService) GetPprof() ([]byte, error) {
	var pfiles Profilestruct
	for _, p := range pprof.Profiles() {
		pfiles.ProfileList = append(pfiles.ProfileList, ProfileData{
			Name:  p.Name(),
			Count: p.Count(),
		})
	}

	return json.Marshal(pfiles)
}

func (slf *PProfService) HTTP_DebugPProf(request *HttpRequest, resp *HttpRespone) error {
	var err error
	resp.Respone, err = slf.GetPprof()
	if err != nil {
		resp.Respone = []byte(fmt.Sprint(err))
	}
	return nil
}

func (slf *PProfService) RPC_DebugPProf(arg *string, ret *Profilestruct) error {

	for _, p := range pprof.Profiles() {
		ret.ProfileList = append(ret.ProfileList, ProfileData{
			Name:  p.Name(),
			Count: p.Count(),
		})
	}

	return nil
}

func (slf *PProfService) HTTP_Test(request *HttpRequest, resp *HttpRespone) error {

	resp.Respone = []byte(request.Body)
	return nil
}
