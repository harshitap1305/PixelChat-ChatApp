import paramiko
import time
import os
import sys

PASSWORD = "12342090"
HOST = "10.1.75.53"

def ssh_exec(port, command, sudo=False):
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    try:
        client.connect(hostname=HOST, port=port, username="student", password=PASSWORD, timeout=10)
        if sudo:
            command = f"echo {PASSWORD} | sudo -S {command}"
        print(f"[{HOST}:{port}] Executing: {command}")
        stdin, stdout, stderr = client.exec_command(command)
        exit_status = stdout.channel.recv_exit_status()
        out = stdout.read().decode('utf-8', errors='replace').encode('ascii', 'ignore').decode('ascii')
        err = stderr.read().decode('utf-8', errors='replace').encode('ascii', 'ignore').decode('ascii')
        if out: print(f"[{HOST}:{port}] STDOUT:\n{out}")
        if err: print(f"[{HOST}:{port}] STDERR:\n{err}")
        return exit_status
    finally:
        client.close()

def ssh_upload(port, local_path, remote_path):
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    try:
        client.connect(hostname=HOST, port=port, username="student", password=PASSWORD, timeout=10)
        sftp = client.open_sftp()
        print(f"[{HOST}:{port}] Uploading {local_path} to {remote_path}")
        
        if os.path.isdir(local_path):
            try:
                sftp.mkdir(remote_path)
            except IOError:
                pass
            for item in os.listdir(local_path):
                lp = os.path.join(local_path, item)
                rp = remote_path + "/" + item
                if os.path.isfile(lp):
                    sftp.put(lp, rp)
        else:
            sftp.put(local_path, remote_path)
        sftp.close()
    finally:
        client.close()

def setup_backend(port_ssh):
    print(f"\n--- Setting up backend on SSH port {port_ssh} ---")
    ssh_exec(port_ssh, "mkdir -p group-chat-app/server group-chat-app/client")
    
    ssh_upload(port_ssh, "server/server.py", "group-chat-app/server/server.py")
    ssh_upload(port_ssh, "server/db.py", "group-chat-app/server/db.py")
    ssh_upload(port_ssh, ".env", "group-chat-app/.env")
    ssh_upload(port_ssh, "cert.pem", "group-chat-app/cert.pem")
    ssh_upload(port_ssh, "key.pem", "group-chat-app/key.pem")
    ssh_upload(port_ssh, "client", "group-chat-app/client")
    
    ssh_exec(port_ssh, "pip3 install --break-system-packages fastapi uvicorn websockets python-multipart bcrypt cryptography python-dotenv")
    ssh_exec(port_ssh, "pkill -f server.py || true")
    
    # Run the server on port 5000
    ssh_exec(port_ssh, "export PORT=5000; nohup python3 group-chat-app/server/server.py > group-chat-app/server.log 2>&1 &")
    time.sleep(2)
    ssh_exec(port_ssh, "curl -k -s https://localhost:5000/health || echo 'Server failed to start'")

def setup_load_balancer(port_ssh):
    print(f"\n--- Setting up Load Balancer on SSH port {port_ssh} ---")
    ssh_exec(port_ssh, "apt update", sudo=True)
    ssh_exec(port_ssh, "apt install -y golang-go", sudo=True)
    
    ssh_exec(port_ssh, "mkdir -p group-chat-app")
    ssh_upload(port_ssh, "load_balancer.go", "group-chat-app/load_balancer.go")
    ssh_upload(port_ssh, "cert.pem", "group-chat-app/cert.pem")
    ssh_upload(port_ssh, "key.pem", "group-chat-app/key.pem")
    
    ssh_exec(port_ssh, "pkill -f load_balancer || true")
    
    # Build LB
    ssh_exec(port_ssh, "cd group-chat-app && go build load_balancer.go")
    
    # Start LB in background
    backends = "https://172.17.0.39:5000,https://172.17.0.40:5000,https://172.17.0.41:5000"
    start_cmd = f"cd group-chat-app && nohup ./load_balancer -backends {backends} -port 8082 > lb.log 2>&1 &"
    ssh_exec(port_ssh, start_cmd)
    time.sleep(2)
    ssh_exec(port_ssh, "curl -s http://localhost:8082/lb/health || echo 'LB failed to start'")

if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "backend":
        setup_backend(2238) # sys2
        setup_backend(2239) # sys3
        setup_backend(2240) # sys4
    elif len(sys.argv) > 1 and sys.argv[1] == "lb":
        setup_load_balancer(2237) # sys1
    else:
        setup_backend(2238)
        setup_backend(2239)
        setup_backend(2240)
        setup_load_balancer(2237)
    print("\nDeployment completed successfully.")
