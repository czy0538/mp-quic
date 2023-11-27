package quic

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/lucas-clemente/quic-go/internal/protocol"
	"github.com/lucas-clemente/quic-go/internal/utils"
	// reuse "github.com/jbenet/go-reuseport"
)

type receivedRawPacket struct {
	rcvPconn   net.PacketConn
	remoteAddr net.Addr
	data       []byte
	rcvTime    time.Time
}

type pconnManager struct {
	// Two kinds of PacketConn: on specific unicast address and the "master"
	// listening on any
	mutex    sync.Mutex
	pconns   map[string]net.PacketConn
	pconnAny net.PacketConn

	localAddrs []net.UDPAddr

	perspective protocol.Perspective

	rcvRawPackets chan *receivedRawPacket

	changePaths chan struct{} // 协调path创建，保证先扫描本地，再尝试创建
	closeConns  chan struct{}
	closed      chan struct{}
	errorConn   chan error
	timer       *time.Timer
}

// Setup the pconn_manager and the pconnAny connection
func (pcm *pconnManager) setup(pconnArg net.PacketConn, listenAddr net.Addr) error {
	pcm.pconns = make(map[string]net.PacketConn)
	pcm.localAddrs = make([]net.UDPAddr, 0)
	pcm.rcvRawPackets = make(chan *receivedRawPacket)
	pcm.changePaths = make(chan struct{}, 1)
	pcm.closeConns = make(chan struct{}, 1)
	pcm.closed = make(chan struct{}, 1)
	pcm.errorConn = make(chan error, 1) // Made non-blocking for tests
	pcm.timer = time.NewTimer(0)

	if pconnArg == nil {
		// XXX (QDC): waiting for native support of SO_REUSEADDR in go...
		//var listenAddrStr string
		//if listenAddr == nil {
		//	listenAddrStr = "[::]:0"
		//} else {
		//	listenAddrStr = listenAddr.String()
		//}
		pconn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		// pconn, err := reuse.ListenPacket("udp", listenAddrStr)
		if err != nil {
			utils.Errorf("pconn_manager: %v", err)
			// Format for expected consistency
			operr := &net.OpError{Op: "listen", Net: "udp", Source: listenAddr, Addr: listenAddr, Err: err}
			return operr
		}
		pcm.pconnAny = pconn
	} else {
		// FIXME Update localAddrs
		pcm.pconnAny = pconnArg
	}

	if utils.Debug() {
		utils.Debugf("Created pconn_manager, any on %s", pcm.pconnAny.LocalAddr().String())
	}

	// Run the pconnManager
	go pcm.run()

	return nil
}

func (pcm *pconnManager) listen(pconn net.PacketConn) {
	var err error

listenLoop:
	for {
		var n int
		var addr net.Addr
		data := getPacketBuffer()
		data = data[:protocol.MaxReceivePacketSize]
		// The packet size should not exceed protocol.MaxReceivePacketSize bytes
		// If it does, we only read a truncate packet, which will then end up undecryptable
		n, addr, err = pconn.ReadFrom(data)
		if err != nil {
			// XXX (QDC): as soon as a path failed, kill the connection.
			// TODO (QDC): be more resilient in the future without breaking expectations
			select {
			case pcm.errorConn <- err:
			default:
				// Don't block
			}
			break listenLoop
			// if pconn == pconnAny {

			// }
			// if !strings.HasSuffix(err.Error(), "use of closed network connection") {
			// TODO
			// c.session.Close(err)
			// }
			// break
		}
		data = data[:n]

		rcvRawPacket := &receivedRawPacket{
			rcvPconn:   pconn,
			remoteAddr: addr,
			data:       data,
			rcvTime:    time.Now(),
		}

		pcm.rcvRawPackets <- rcvRawPacket
	}
}

func (pcm *pconnManager) run() {
	// First start to listen to the sockets
	go pcm.listen(pcm.pconnAny)
	// XXX (QDC): maybe wait for one handshake to complete, but maybe not needed
	// FIXME Server starting on any vs. server with non-any address
	if pcm.perspective == protocol.PerspectiveClient {
		pcm.createPconns()
	}

	// 通告进行路径创建
	select {
	case pcm.changePaths <- struct{}{}:
	default:
	}
	// Start the timer for periodic interface checking (only for client)
	duration, _ := time.ParseDuration("2s")
	if pcm.perspective == protocol.PerspectiveClient {
		pcm.timer.Reset(duration)
	} else {
		if !pcm.timer.Stop() {
			<-pcm.timer.C
		}
	}
	// 构建了一个2s的定时器，每两秒进行一次检查
runLoop:
	for {
		select {
		case <-pcm.closeConns:
			break runLoop
		case <-pcm.timer.C:
			pcm.createPconns()
			pcm.timer.Reset(duration)
		}
	}
	// Close pconns
	pcm.closePconns()
}

func (pcm *pconnManager) createPconn(ip net.IP) (*net.UDPAddr, error) {
	// XXX (QDC): waiting for native support of SO_REUSEADDR in go...
	//var listenAddrStr string
	//if ip.To4() != nil {
	//	listenAddrStr = ip.String() + ":0"
	//} else {
	//	listenAddrStr = "[" + ip.String() + "]:0"
	//}
	// pconn, err := reuse.ListenPacket("udp", listenAddrStr)
	pconn, err := net.ListenUDP("udp", &net.UDPAddr{IP: ip, Port: 0})
	if err != nil {
		return nil, err
	}
	locAddr, err := net.ResolveUDPAddr("udp", pconn.LocalAddr().String())
	if err != nil {
		return nil, err
	}
	pcm.mutex.Lock()
	pcm.pconns[locAddr.String()] = pconn
	pcm.mutex.Unlock()
	if utils.Debug() {
		utils.Debugf("Created pconn on %s", pconn.LocalAddr().String())
	}
	// Start to listen on this new socket
	go pcm.listen(pconn)
	// Don't block
	select {
	case pcm.changePaths <- struct{}{}:
	default:
	}
	return locAddr, nil
}

func (pcm *pconnManager) createPconns() error {
	// 获取本地的所有的接口
	ifaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, i := range ifaces {
		// TODO (QDC): do this in a generic way
		// 根据接口名进行第一波筛选
		// 加入Tailscale的接口和软总线接口
		if !strings.Contains(i.Name, "softbus") && !strings.Contains(i.Name, "tailscale") && !strings.Contains(i.Name, "eth") && !strings.Contains(i.Name, "rmnet") && !strings.Contains(i.Name, "wlan") {
			continue
		}
		// 解析他们的地址
		addrs, err := i.Addrs()
		if err != nil {
			return err
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err != nil {
				return err
			}
			// If not Global Unicast, bypass
			if !ip.IsGlobalUnicast() {
				continue
			}
			// TODO (QDC): Clearly not optimal
			found := false
			// 判断该地址是否已经被添加
		lookingLoop:
			for _, locAddr := range pcm.localAddrs {
				if ip.Equal(locAddr.IP) {
					found = true
					break lookingLoop
				}
			}
			// 创建Pconn，并追加入本地地址
			if !found {
				// 创建udp connection，并进行侦听
				// 同时将描述符加入pconns
				locAddr, err := pcm.createPconn(ip)
				if err != nil {
					return err
				}
				pcm.localAddrs = append(pcm.localAddrs, *locAddr)
				utils.Debugf("Added local address %s", locAddr.String())
			}
		}
	}
	// begin
	// 强行添加本地端口
	// ip := net.ParseIP("100.64.1.150")
	//ip := net.ParseIP("192.168.1.50")
	//for _, locAddr := range pcm.localAddrs {
	//	if ip.Equal(locAddr.IP) {
	//		return nil
	//	}
	//}
	//locAddr, err := pcm.createPconn(ip)
	//if err != nil {
	//	return err
	//}
	//pcm.localAddrs = append(pcm.localAddrs, *locAddr)
	//utils.Debugf("Added local address %s", locAddr.String())
	// end
	return nil
}

func (pcm *pconnManager) closePconns() {
	for _, pconn := range pcm.pconns {
		pconn.Close()
	}
	pcm.pconnAny.Close()
	close(pcm.closed)
}
