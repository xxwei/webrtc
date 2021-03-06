package webrtc

import (
	"github.com/pions/rtcp"
	"github.com/pions/rtp"
	"github.com/pions/webrtc/pkg/media"
)

const rtpOutboundMTU = 1400

// RTCRtpSender allows an application to control how a given RTCTrack is encoded and transmitted to a remote peer
type RTCRtpSender struct {
	Track *RTCTrack

	transport *RTCDtlsTransport
}

// NewRTCRtpSender constructs a new RTCRtpSender
func NewRTCRtpSender(track *RTCTrack, transport *RTCDtlsTransport) *RTCRtpSender {
	r := &RTCRtpSender{
		Track:     track,
		transport: transport,
	}

	r.Track.sampleInput = make(chan media.RTCSample, 15) // Is the buffering needed?
	r.Track.rawInput = make(chan *rtp.Packet, 15)        // Is the buffering needed?
	r.Track.rtcpInput = make(chan rtcp.Packet, 15)       // Is the buffering needed?

	r.Track.Samples = r.Track.sampleInput
	r.Track.RawRTP = r.Track.rawInput
	r.Track.RTCPPackets = r.Track.rtcpInput

	if r.Track.isRawRTP {
		close(r.Track.Samples)
	} else {
		close(r.Track.RawRTP)
	}

	return r
}

// Send Attempts to set the parameters controlling the sending of media.
func (r *RTCRtpSender) Send(parameters RTCRtpSendParameters) {
	if r.Track.isRawRTP {
		go r.handleRawRTP(r.Track.rawInput)
	} else {
		go r.handleSampleRTP(r.Track.sampleInput)
	}

	go r.handleRTCP(r.transport, r.Track.rtcpInput)
}

// Stop irreversibly stops the RTCRtpSender
func (r *RTCRtpSender) Stop() {
	if r.Track.isRawRTP {
		close(r.Track.RawRTP)
	} else {
		close(r.Track.Samples)
	}

	// TODO properly tear down all loops (and test that)
}

func (r *RTCRtpSender) handleRawRTP(rtpPackets chan *rtp.Packet) {
	for {
		p, ok := <-rtpPackets
		if !ok {
			return
		}

		r.sendRTP(p)
	}
}

func (r *RTCRtpSender) handleSampleRTP(rtpPackets chan media.RTCSample) {
	packetizer := rtp.NewPacketizer(
		rtpOutboundMTU,
		r.Track.PayloadType,
		r.Track.Ssrc,
		r.Track.Codec.Payloader,
		rtp.NewRandomSequencer(),
		r.Track.Codec.ClockRate,
	)

	for {
		in, ok := <-rtpPackets
		if !ok {
			return
		}
		packets := packetizer.Packetize(in.Data, in.Samples)
		for _, p := range packets {
			r.sendRTP(p)
		}
	}

}

func (r *RTCRtpSender) handleRTCP(transport *RTCDtlsTransport, rtcpPackets chan rtcp.Packet) {
	srtcpSession, err := transport.getSRTCPSession()
	if err != nil {
		pcLog.Warnf("Failed to open SRTCPSession, RTCTrack done for: %v %d \n", err, r.Track.Ssrc)
		return
	}

	readStream, err := srtcpSession.OpenReadStream(r.Track.Ssrc)
	if err != nil {
		pcLog.Warnf("Failed to open RTCP ReadStream, RTCTrack done for: %v %d \n", err, r.Track.Ssrc)
		return
	}

	var rtcpPacket rtcp.Packet
	for {
		rtcpBuf := make([]byte, receiveMTU)
		i, err := readStream.Read(rtcpBuf)
		if err != nil {
			pcLog.Warnf("Failed to read, RTCTrack done for: %v %d \n", err, r.Track.Ssrc)
			return
		}

		rtcpPacket, _, err = rtcp.Unmarshal(rtcpBuf[:i])
		if err != nil {
			pcLog.Warnf("Failed to unmarshal RTCP packet, discarding: %v \n", err)
			continue
		}

		select {
		case rtcpPackets <- rtcpPacket:
		default:
		}
	}

}

func (r *RTCRtpSender) sendRTP(packet *rtp.Packet) {
	srtpSession, err := r.transport.getSRTPSession()
	if err != nil {
		pcLog.Warnf("SendRTP failed to open SrtpSession: %v", err)
		return
	}

	writeStream, err := srtpSession.OpenWriteStream()
	if err != nil {
		pcLog.Warnf("SendRTP failed to open WriteStream: %v", err)
		return
	}

	if _, err := writeStream.WriteRTP(&packet.Header, packet.Payload); err != nil {
		pcLog.Warnf("SendRTP failed to write: %v", err)
	}
}
