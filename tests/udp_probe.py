import socket
import struct
import sys


def recv_exact(sock, count):
    data = b""
    while len(data) < count:
        chunk = sock.recv(count - len(data))
        if not chunk:
            raise EOFError(f"EOF after {len(data)}/{count} bytes")
        data += chunk
    return data


def udp_dns_probe(port):
    s = socket.create_connection(("127.0.0.1", port), timeout=20)
    try:
        s.sendall(b"SNET\x01\x00")
        status = recv_exact(s, 1)[0]
        if status != 0:
            return f"handshake status {status}"
        s.sendall(b"\x02")

        txid = 0x5151
        query = struct.pack("!HHHHHH", txid, 0x0100, 1, 0, 0, 0)
        query += b"\x07example\x03com\x00" + struct.pack("!HH", 1, 1)
        body = b"\x01" + socket.inet_aton("9.9.9.9") + struct.pack("!H", 53) + query
        s.sendall(struct.pack("!H", len(body)) + body)

        header = recv_exact(s, 2)
        frame_length = struct.unpack("!H", header)[0]
        frame = recv_exact(s, frame_length)
        atyp = frame[0]
        if atyp == 1:
            reply_from = socket.inet_ntoa(frame[1:5])
            reply_port = struct.unpack("!H", frame[5:7])[0]
            offset = 7
        elif atyp == 4:
            reply_from = socket.inet_ntop(socket.AF_INET6, frame[1:17])
            reply_port = struct.unpack("!H", frame[17:19])[0]
            offset = 19
        else:
            length = frame[1]
            reply_from = frame[2:2 + length].decode()
            reply_port = struct.unpack("!H", frame[2 + length:4 + length])[0]
            offset = 4 + length

        payload = frame[offset:]
        rid, flags = struct.unpack("!HH", payload[:4])
        return (
            f"frame={frame_length}B from={reply_from}:{reply_port} "
            f"id={rid:#06x} rcode={flags & 0xf} payload={len(payload)}B"
        )
    finally:
        s.close()


failures = 0
for label, port in (("abstract", 30000), ("fallback", 30002)):
    try:
        print(f"PASS  {label}: {udp_dns_probe(port)}")
    except Exception as error:
        failures += 1
        print(f"FAIL  {label}: {type(error).__name__}: {error}")

sys.exit(1 if failures else 0)
