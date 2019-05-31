package t125

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/chuckpreslar/emission"
	"github.com/icodeface/grdp/core"
	"github.com/icodeface/grdp/glog"
	"github.com/icodeface/grdp/protocol/t125/ber"
	"github.com/icodeface/grdp/protocol/t125/gcc"
	"github.com/icodeface/grdp/protocol/t125/per"
	"github.com/icodeface/grdp/protocol/x224"
	"io"
)

// take idea from https://github.com/Madnikulin50/gordp

// Multiple Channel Service layer

type MCSMessage uint8

const (
	MCS_TYPE_CONNECT_INITIAL  MCSMessage = 0x65
	MCS_TYPE_CONNECT_RESPONSE            = 0x66
)

type MCSDomainPDU uint16

const (
	ERECT_DOMAIN_REQUEST          MCSDomainPDU = 1
	DISCONNECT_PROVIDER_ULTIMATUM              = 8
	ATTACH_USER_REQUEST                        = 10
	ATTACH_USER_CONFIRM                        = 11
	CHANNEL_JOIN_REQUEST                       = 14
	CHANNEL_JOIN_CONFIRM                       = 15
	SEND_DATA_REQUEST                          = 25
	SEND_DATA_INDICATION                       = 26
)

type MCSChannel uint16

const (
	MCS_GLOBAL_CHANNEL   MCSChannel = 1003
	MCS_USERCHANNEL_BASE            = 1001
)

/**
 * Format MCS PDU header packet
 * @param mcsPdu {integer}
 * @param options {integer}
 * @returns {type.UInt8} headers
 */
func writeMCSPDUHeader(mcsPdu MCSDomainPDU, options uint8, w io.Writer) {
	core.WriteUInt8((uint8(mcsPdu)<<2)|options, w)
}

func readMCSPDUHeader(options uint8, mcsPdu MCSDomainPDU) bool {
	return (options >> 2) == uint8(mcsPdu)
}

type DomainParameters struct {
	MaxChannelIds   int `asn1: "tag:2"`
	MaxUserIds      int `asn1: "tag:2"`
	MaxTokenIds     int `asn1: "tag:2"`
	NumPriorities   int `asn1: "tag:2"`
	MinThoughput    int `asn1: "tag:2"`
	MaxHeight       int `asn1: "tag:2"`
	MaxMCSPDUsize   int `asn1: "tag:2"`
	ProtocolVersion int `asn1: "tag:2"`
}

/**
 * @see http://www.itu.int/rec/T-REC-T.125-199802-I/en page 25
 * @returns {asn1.univ.Sequence}
 */
func NewDomainParameters(maxChannelIds int,
	maxUserIds int,
	maxTokenIds int,
	numPriorities int,
	minThoughput int,
	maxHeight int,
	maxMCSPDUsize int,
	protocolVersion int) *DomainParameters {
	return &DomainParameters{maxChannelIds, maxUserIds, maxTokenIds,
		numPriorities, minThoughput, maxHeight, maxMCSPDUsize, protocolVersion}
}

func (d *DomainParameters) BER() []byte {
	buff := &bytes.Buffer{}
	ber.WriteInteger(d.MaxChannelIds, buff)
	ber.WriteInteger(d.MaxUserIds, buff)
	ber.WriteInteger(d.MaxTokenIds, buff)
	ber.WriteInteger(1, buff)
	ber.WriteInteger(0, buff)
	ber.WriteInteger(1, buff)
	ber.WriteInteger(d.MaxMCSPDUsize, buff)
	ber.WriteInteger(2, buff)
	return buff.Bytes()
}

/**
 * @see http://www.itu.int/rec/T-REC-T.125-199802-I/en page 25
 * @param userData {Buffer}
 * @returns {asn1.univ.Sequence}
 */
type ConnectInitial struct {
	CallingDomainSelector []byte `asn1: "tag:4"`
	CalledDomainSelector  []byte `asn1: "tag:4"`
	UpwardFlag            bool
	TargetParameters      DomainParameters
	MinimumParameters     DomainParameters
	MaximumParameters     DomainParameters
	UserData              []byte `asn1: "application, tag:101"`
}

func NewConnectInitial(userData []byte) ConnectInitial {
	return ConnectInitial{[]byte{0x1},
		[]byte{0x1},
		true,
		*NewDomainParameters(34, 2, 0, 1, 0, 1, 0xffff, 2),
		*NewDomainParameters(1, 1, 1, 1, 0, 1, 0x420, 2),
		*NewDomainParameters(0xffff, 0xfc17, 0xffff, 1, 0, 1, 0xffff, 2),
		userData}
}

func (c *ConnectInitial) BER() []byte {
	buff := &bytes.Buffer{}
	ber.WriteOctetstring(string(c.CallingDomainSelector), buff)
	ber.WriteOctetstring(string(c.CalledDomainSelector), buff)
	ber.WriteBoolean(c.UpwardFlag, buff)
	ber.WriteEncodedDomainParams(c.TargetParameters.BER(), buff)
	ber.WriteEncodedDomainParams(c.MinimumParameters.BER(), buff)
	ber.WriteEncodedDomainParams(c.MaximumParameters.BER(), buff)
	ber.WriteOctetstring(string(c.UserData), buff)
	return buff.Bytes()
}

/**
 * @see http://www.itu.int/rec/T-REC-T.125-199802-I/en page 25
 * @returns {asn1.univ.Sequence}
 */

type ConnectResponse struct {
	result           int `asn1: "tag:10"`
	calledConnectId  int
	domainParameters DomainParameters
	userData         []byte `asn1: "tag:10"`
}

func NewConnectResponse(userData []byte) *ConnectResponse {
	return &ConnectResponse{0,
		0,
		*NewDomainParameters(22, 3, 0, 1, 0, 1, 0xfff8, 2),
		userData}
}

func ReadConnectResponse(r io.Reader) (*ConnectResponse, error) {
	// todo
	return NewConnectResponse([]byte{}), nil
}

type MCSChannelInfo struct {
	id   MCSChannel
	name string
}

type MCS struct {
	emission.Emitter
	transport  core.Transport
	recvOpCode MCSDomainPDU
	sendOpCode MCSDomainPDU
	channels   []MCSChannelInfo
}

func NewMCS(t core.Transport, recvOpCode MCSDomainPDU, sendOpCode MCSDomainPDU) *MCS {
	m := &MCS{
		*emission.NewEmitter(),
		t,
		recvOpCode,
		sendOpCode,
		[]MCSChannelInfo{{MCS_GLOBAL_CHANNEL, "global"}},
	}

	m.transport.On("close", func() {
		m.Emit("close")
	}).On("error", func(err error) {
		m.Emit("error", err)
	})
	return m
}

func (x *MCS) Read(b []byte) (n int, err error) {
	return x.transport.Read(b)
}

func (x *MCS) Write(b []byte) (n int, err error) {
	return x.transport.Write(b)
}

func (m *MCS) Close() error {
	return m.transport.Close()
}

type MCSClient struct {
	*MCS
	clientCoreData     *gcc.ClientCoreData
	clientNetworkData  *gcc.ClientNetworkData
	clientSecurityData *gcc.ClientSecurityData

	serverCoreData     *gcc.ServerCoreData
	serverNetworkData  *gcc.ServerNetworkData
	serverSecurityData *gcc.ServerSecurityData

	channelsConnected int
	userId            uint16
}

func NewMCSClient(t core.Transport) *MCSClient {
	c := &MCSClient{
		MCS:                NewMCS(t, SEND_DATA_INDICATION, SEND_DATA_REQUEST),
		clientCoreData:     gcc.NewClientCoreData(),
		clientNetworkData:  gcc.NewClientNetworkData(),
		clientSecurityData: gcc.NewClientSecurityData(),
	}
	c.transport.On("connect", c.connect)
	return c
}

func (c *MCSClient) connect(selectedProtocol x224.Protocol) {
	glog.Debug("mcs client on connect", selectedProtocol)
	c.clientCoreData.ServerSelectedProtocol = uint32(selectedProtocol)

	// sendConnectInitial
	userDataBuff := bytes.Buffer{}
	userDataBuff.Write(c.clientCoreData.Block())
	userDataBuff.Write(c.clientNetworkData.Block())
	userDataBuff.Write(c.clientSecurityData.Block())

	ccReq := gcc.MakeConferenceCreateRequest(userDataBuff.Bytes())
	connectInitial := NewConnectInitial(ccReq)
	connectInitialBerEncoded := connectInitial.BER()

	dataBuff := &bytes.Buffer{}
	ber.WriteApplicationTag(uint8(MCS_TYPE_CONNECT_INITIAL), len(connectInitialBerEncoded), dataBuff)
	dataBuff.Write(connectInitialBerEncoded)

	_, err := c.transport.Write(dataBuff.Bytes())
	if err != nil {
		c.Emit("error", errors.New(fmt.Sprintf("mcs sendConnectInitial write error %v", err)))
		return
	}
	glog.Debug("mcs wait for data event")
	c.transport.Once("data", c.recvConnectResponse)
}

func (c *MCSClient) recvConnectResponse(s []byte) {
	glog.Debug("mcs recvConnectResponse", hex.EncodeToString(s))
	// todo
	cResp, err := ReadConnectResponse(bytes.NewReader(s))
	if err != nil {
		glog.Error(err)
		c.Emit("error", err)
		return
	}

	// record server gcc block
	serverSettings := gcc.ReadConferenceCreateResponse(cResp.userData)
	for _, v := range serverSettings {
		switch v.(type) {
		case gcc.ServerSecurityData:
			{
				c.serverSecurityData = v.(*gcc.ServerSecurityData)
			}
		case gcc.ServerCoreData:
			{
				c.serverCoreData = v.(*gcc.ServerCoreData)
			}
		case gcc.ServerNetworkData:
			{
				c.serverNetworkData = v.(*gcc.ServerNetworkData)
			}
		default:
			err := errors.New(fmt.Sprintf("unhandle server gcc block %v %v", v, cResp.userData))
			glog.Error(err)
			c.Emit("error", err)
			return
		}
	}

	glog.Debug("mcs sendErectDomainRequest")
	c.sendErectDomainRequest()

	glog.Debug("mcs sendAttachUserRequest")
	c.sendAttachUserRequest()

	c.transport.Once("data", c.recvAttachUserConfirm)
}

func (c *MCSClient) sendErectDomainRequest() {
	buff := &bytes.Buffer{}
	writeMCSPDUHeader(ERECT_DOMAIN_REQUEST, 0, buff)
	per.WriteInteger(0, buff)
	per.WriteInteger(0, buff)
	c.transport.Write(buff.Bytes())
}

func (c *MCSClient) sendAttachUserRequest() {
	buff := &bytes.Buffer{}
	writeMCSPDUHeader(ATTACH_USER_REQUEST, 0, buff)
	c.transport.Write(buff.Bytes())
}

func (c *MCSClient) recvAttachUserConfirm(s []byte) {
	glog.Debug("mcs recvAttachUserConfirm")
	r := bytes.NewReader(s)

	option, err := core.ReadUInt8(r)
	if err != nil {
		c.Emit("error", err)
		return
	}

	if !readMCSPDUHeader(option, ATTACH_USER_CONFIRM) {
		c.Emit("error", errors.New("NODE_RDP_PROTOCOL_T125_MCS_BAD_HEADER"))
		return
	}

	e, err := per.ReadEnumerates(r)
	if err != nil {
		c.Emit("error", err)
		return
	}
	if e != 0 {
		c.Emit("error", errors.New("NODE_RDP_PROTOCOL_T125_MCS_SERVER_REJECT_USER'"))
		return
	}

	userId, _ := per.ReadInteger16(r)
	userId += MCS_USERCHANNEL_BASE
	c.userId = userId

	c.channels = append(c.channels, MCSChannelInfo{MCSChannel(userId), "user"})
	c.connectChannels()
}

func (c *MCSClient) connectChannels() {
	// todo
	glog.Debug("mcs connectChannels")
	if c.channelsConnected == len(c.channels) {
		glog.Debug("msc connectChannels callback to sec")
		c.transport.On("data", func(s []byte) {

		})
		// send client and sever gcc informations
		// callback to sec
		c.Emit("connect", c.userId, c.channels)
	}

	// sendChannelJoinRequest
	c.sendChannelJoinRequest(c.channels[c.channelsConnected].id)
	c.channelsConnected += 1
	c.transport.Once("data", c.recvChannelJoinConfirm)
}

func (c *MCSClient) sendChannelJoinRequest(channelId MCSChannel) {
	glog.Debug("mcs sendChannelJoinRequest")
}

func (c *MCSClient) recvChannelJoinConfirm(s []byte) {
	// todo
	glog.Debug("mcs recvChannelJoinConfirm")
	c.connectChannels()
}