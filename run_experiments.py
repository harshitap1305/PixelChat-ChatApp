import paramiko
import time
import os

PASSWORD = "12342090"
HOST = "10.1.75.53"
PORT_SYS1 = 2237

def ssh_exec(command):
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(hostname=HOST, port=PORT_SYS1, username="student", password=PASSWORD, timeout=10)
    print(f"[Sys1] Executing: {command}")
    stdin, stdout, stderr = client.exec_command(command)
    exit_status = stdout.channel.recv_exit_status()
    out = stdout.read().decode('utf-8', errors='replace').encode('ascii', 'ignore').decode('ascii').strip()
    client.close()
    return out

def ssh_upload(local_path, remote_path):
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(hostname=HOST, port=PORT_SYS1, username="student", password=PASSWORD, timeout=10)
    sftp = client.open_sftp()
    sftp.put(local_path, remote_path)
    sftp.close()
    client.close()

print("Uploading load_generator.go...")
ssh_upload("load_generator.go", "group-chat-app/load_generator.go")

print("Building load_generator...")
out = ssh_exec("cd group-chat-app && go build load_generator.go")
print(out)

print("--- Experiment 1: Single Backend (172.17.0.39) ---")
# Start LB with only Sys2
ssh_exec("pkill -f load_balancer || true")
ssh_exec("nohup ./group-chat-app/load_balancer -backends http://172.17.0.39:8081 -port 8080 > group-chat-app/lb.log 2>&1 &")
time.sleep(2)
# Run Load Generator targeting LB
print("Running Load Generator...")
out = ssh_exec("cd group-chat-app && ./load_generator -url http://localhost:8080/ -requests 5000 -concurrency 40 -experiment 1_backend")
print(out)

print("\n--- Experiment 2: Three Backends (172.17.0.39, 172.17.0.40, 172.17.0.41) ---")
# Start LB with all three
ssh_exec("pkill -f load_balancer || true")
ssh_exec("nohup ./group-chat-app/load_balancer -backends http://172.17.0.39:8081,http://172.17.0.40:8081,http://172.17.0.41:8081 -port 8080 > group-chat-app/lb.log 2>&1 &")
time.sleep(2)
# Run Load Generator targeting LB
print("Running Load Generator...")
out = ssh_exec("cd group-chat-app && ./load_generator -url http://localhost:8080/ -requests 5000 -concurrency 40 -experiment 3_backends")
print(out)

# Download the CSV
client = paramiko.SSHClient()
client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
client.connect(hostname=HOST, port=PORT_SYS1, username="student", password=PASSWORD, timeout=10)
sftp = client.open_sftp()
sftp.get("group-chat-app/results.csv", "results.csv")
sftp.close()
client.close()

print("\n--- Downloaded results.csv ---")
with open("results.csv") as f:
    print(f.read())
