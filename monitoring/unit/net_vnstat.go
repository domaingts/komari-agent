package monitoring

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"os"
	"os/exec"
	"time"
)

var (
	networkUp   uint64 = 0
	networkDown uint64 = 0
	basePath    string = "./.base.gob"
)

func init() {
	setBaseTraffic()
}

type Base struct {
	NetworkUp   uint64
	NetworkDown uint64
}

func setBaseTraffic() {
	gob.Register(Base{})
	_, err := os.Stat("/usr/bin/vnstat")
	o, err := exec.Command("/usr/bin/vnstat", "--json", "m").Output()
	if err != nil {
		return
	}
	var result VnstatOutput
	err = json.Unmarshal(o, &result)
	if err != nil {
		return
	}
	var up, down uint64
	var timestamp int64
	for _, iface := range result.Interfaces {
		down += iface.Traffic.Total.Rx
		up += iface.Traffic.Total.Tx
		if iface.Updated.Timestamp > timestamp {
			timestamp = iface.Updated.Timestamp
		}
	}
	ut, err := Uptime()
	if err != nil {
		return
	}

	if time.Since(time.Unix(timestamp, 0)) > time.Duration(ut)*time.Second {
		networkUp = up
		networkDown = down
		var b bytes.Buffer
		err := gob.NewEncoder(&b).Encode(Base{NetworkUp: networkUp, NetworkDown: networkDown})
		if err != nil {
			return
		}
		err = os.WriteFile(basePath, b.Bytes(), 0666)
		if err != nil {
			return
		}
	} else {
		result, err := os.ReadFile(basePath)
		if err != nil || len(result) == 0 {
			return
		}
		var b Base
		err = gob.NewDecoder(bytes.NewReader(result)).Decode(&b)
		if err != nil {
			return
		}
		networkUp = b.NetworkUp
		networkDown = b.NetworkDown
	}
}
