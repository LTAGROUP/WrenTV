package direct

import (
	"errors"
	"math/rand/v2"
	"net"
	"time"

	alog "github.com/anacrolix/log"
	"github.com/anacrolix/upnp"
	"github.com/jackpal/gateway"
	natpmp "github.com/jackpal/go-nat-pmp"
)

const mappingLease = 2 * time.Hour

// portMapper keeps a TCP port forwarded on the home router with UPnP IGD or
// NAT-PMP, whichever the router answers.
type portMapper struct {
	internal int
	external int    // last mapped external port (kept stable across renewals)
	via      string // "upnp" or "nat-pmp"
	upnpDev  upnp.Device
	pmp      *natpmp.Client
}

// mapping returns the external port and the router's own WAN address (nil
// when the router didn't say).
func (p *portMapper) mapping() (int, net.IP, error) {
	want := p.external
	if want == 0 {
		want = p.internal
	}
	if port, ip, err := p.viaUPnP(want); err == nil {
		p.external, p.via = port, "upnp"
		return port, ip, nil
	}
	if port, ip, err := p.viaNATPMP(want); err == nil {
		p.external, p.via = port, "nat-pmp"
		return port, ip, nil
	}
	p.via = ""
	return 0, nil, errors.New("the router didn't accept a UPnP or NAT-PMP port mapping")
}

func (p *portMapper) viaUPnP(want int) (int, net.IP, error) {
	if p.upnpDev == nil {
		devs := upnp.Discover(0, 3*time.Second, alog.Default.WithFilterLevel(alog.Disabled))
		if len(devs) == 0 {
			return 0, nil, errors.New("no UPnP router")
		}
		p.upnpDev = devs[0]
	}
	d := p.upnpDev
	port, err := d.AddPortMapping(upnp.TCP, p.internal, want, "Wren TV", mappingLease)
	if err != nil {
		// Taken by another device or rule: try one random high port.
		port, err = d.AddPortMapping(upnp.TCP, p.internal, 20000+rand.IntN(40000), "Wren TV", mappingLease)
	}
	if err != nil {
		p.upnpDev = nil
		return 0, nil, err
	}
	ip, _ := d.GetExternalIPAddress()
	return port, ip, nil
}

func (p *portMapper) viaNATPMP(want int) (int, net.IP, error) {
	if p.pmp == nil {
		gw, err := gateway.DiscoverGateway()
		if err != nil {
			return 0, nil, err
		}
		p.pmp = natpmp.NewClientWithTimeout(gw, 3*time.Second)
	}
	res, err := p.pmp.AddPortMapping("tcp", p.internal, want, int(mappingLease/time.Second))
	if err != nil {
		p.pmp = nil
		return 0, nil, err
	}
	var ip net.IP
	if ext, err := p.pmp.GetExternalAddress(); err == nil {
		ip = net.IP(ext.ExternalIPAddress[:])
	}
	return int(res.MappedExternalPort), ip, nil
}

// remove deletes the mapping (best effort, on shutdown).
func (p *portMapper) remove() {
	switch {
	case p.via == "upnp" && p.upnpDev != nil:
		_ = p.upnpDev.DeletePortMapping(upnp.TCP, p.external)
	case p.via == "nat-pmp" && p.pmp != nil:
		_, _ = p.pmp.AddPortMapping("tcp", p.internal, 0, 0)
	}
}
