import paramiko
import sys
import threading
import select
import socket
import socketserver

def handler(chan, host, port):
    sock = socket.socket()
    try:
        sock.connect((host, port))
    except Exception as e:
        print(f"Forwarding request to {host}:{port} failed: {e}")
        return
    
    print(f"Connected! Tunnel open {chan.origin_addr} -> {chan.getpeername()} -> {host}:{port}")
    while True:
        r, w, x = select.select([sock, chan], [], [])
        if sock in r:
            data = sock.recv(1024)
            if len(data) == 0: break
            chan.send(data)
        if chan in r:
            data = chan.recv(1024)
            if len(data) == 0: break
            sock.send(data)
    chan.close()
    sock.close()
    print("Tunnel closed")

def reverse_forward_tunnel(server_port, remote_host, remote_port, transport):
    transport.request_port_forward('', server_port)
    while True:
        chan = transport.accept(1000)
        if chan is None:
            continue
        thr = threading.Thread(target=handler, args=(chan, remote_host, remote_port))
        thr.setDaemon(True)
        thr.start()

def forward_tunnel(local_port, remote_host, remote_port, transport):
    class SubHander(socketserver.BaseRequestHandler):
        def handle(self):
            try:
                chan = transport.open_channel('direct-tcpip',
                                              (remote_host, remote_port),
                                              self.request.getpeername())
            except Exception as e:
                return
            if chan is None:
                return
            while True:
                r, w, x = select.select([self.request, chan], [], [])
                if self.request in r:
                    data = self.request.recv(1024)
                    if len(data) == 0: break
                    chan.send(data)
                if chan in r:
                    data = chan.recv(1024)
                    if len(data) == 0: break
                    self.request.send(data)
            chan.close()
            self.request.close()
    
    class ThreadingTCPServer(socketserver.ThreadingMixIn, socketserver.TCPServer): pass
    server = ThreadingTCPServer(('', local_port), SubHander)
    server.serve_forever()

if __name__ == '__main__':
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    print("Connecting...")
    client.connect('10.1.75.53', 2237, username='student', password='12342090')
    print("Port forwarding 8080 -> 10.1.75.53:8080")
    try:
        forward_tunnel(8080, '127.0.0.1', 8080, client.get_transport())
    except KeyboardInterrupt:
        print("Exiting...")
        sys.exit(0)
