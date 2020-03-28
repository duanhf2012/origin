package service

//本地所有的service
var mapServiceName map[string]IService

func init(){
	mapServiceName = map[string]IService{}
}

func Init(chanCloseSig chan bool) {
	closeSig=chanCloseSig

	for _,s := range mapServiceName {
		s.OnInit()
	}
}


func Setup(s IService) bool {

	_,ok := mapServiceName[s.GetName()]
	if ok == true {
		return false
	}

	//s.Init(s)
	mapServiceName[s.GetName()] = s

	return true
}

func GetService(servicename string) IService {
	s,ok := mapServiceName[servicename]
	if ok == false {
		return nil
	}

	return s
}


func Start(){
	for _,s := range mapServiceName {
		s.Start()
	}
}

func WaitStop(){
	for _,s := range mapServiceName {
		s.Wait()
	}
}

