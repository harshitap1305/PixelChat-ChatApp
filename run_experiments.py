import paramiko
import time

PASSWORD = "12342090"
HOST = "10.1.75.53"
PORT_SYS1 = 2237

BACKEND_IPS = {
    "sys2": "172.17.0.39",
    "sys3": "172.17.0.40",
    "sys4": "172.17.0.41",
}

def ssh_exec(command, port=PORT_SYS1, timeout_sec=300):
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(hostname=HOST, port=port, username="student", password=PASSWORD, timeout=10)
    print(f"[Sys1:{port}] Executing: {command}")
    stdin, stdout, stderr = client.exec_command(command, timeout=timeout_sec)
    exit_status = stdout.channel.recv_exit_status()
    out = stdout.read().decode('utf-8', errors='replace').encode('ascii', 'ignore').decode('ascii').strip()
    client.close()
    return out, exit_status

# Clean old results
print("=== Cleaning old results ===")
ssh_exec("rm -f group-chat-app/results.csv group-chat-app/*.json")

# ──────────────────────────────────────────────────────
# EXPERIMENT 1: Single Backend under heavy load
# ──────────────────────────────────────────────────────
print("\n" + "="*60)
print("=== EXPERIMENT 1: Single Backend (Sys2 only) ===")
print("="*60)

ssh_exec("pkill -f load_balancer || true")
time.sleep(1)

sys2_ip = BACKEND_IPS["sys2"]
ssh_exec(
    f"nohup ./group-chat-app/load_balancer "
    f"-backends http://{sys2_ip}:8081 -port 8080 -backend-timeout 500ms "
    f"> group-chat-app/lb.log 2>&1 &"
)
time.sleep(3)

out, _ = ssh_exec("curl -s http://localhost:8080/lb/status")
print(f"  LB Status: {out}")

print("\nRunning Load Generator...")
out, _ = ssh_exec(
    "cd group-chat-app && ./load_generator "
    "-url 'http://localhost:8080/?delay=100ms' "
    "-requests 5000 -concurrency 200 -experiment 1_backend -timeout 3s",
    timeout_sec=300
)
print(f"  Result: {out}")

# Get LB metrics for experiment 1
print("\n  LB Metrics (Exp 1):")
out, _ = ssh_exec("curl -s http://localhost:8080/lb/metrics")
print(f"  {out}")

time.sleep(3)

# ──────────────────────────────────────────────────────
# EXPERIMENT 2: Three Backends
# ──────────────────────────────────────────────────────
print("\n" + "="*60)
print("=== EXPERIMENT 2: Three Backends (Sys2 + Sys3 + Sys4) ===")
print("="*60)

ssh_exec("pkill -f load_balancer || true")
time.sleep(1)

all_backends = ",".join([f"http://{ip}:8081" for ip in BACKEND_IPS.values()])
ssh_exec(
    f"nohup ./group-chat-app/load_balancer "
    f"-backends {all_backends} -port 8080 -backend-timeout 500ms "
    f"> group-chat-app/lb.log 2>&1 &"
)
time.sleep(3)

out, _ = ssh_exec("curl -s http://localhost:8080/lb/status")
print(f"  LB Status: {out}")

print("\nRunning Load Generator...")
out, _ = ssh_exec(
    "cd group-chat-app && ./load_generator "
    "-url 'http://localhost:8080/?delay=100ms' "
    "-requests 5000 -concurrency 200 -experiment 3_backends -timeout 3s",
    timeout_sec=300
)
print(f"  Result: {out}")

print("\n  LB Metrics (Exp 2):")
out, _ = ssh_exec("curl -s http://localhost:8080/lb/metrics")
print(f"  {out}")

# ── Download results ──
print("\n=== Downloading results.csv ===")
client = paramiko.SSHClient()
client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
client.connect(hostname=HOST, port=PORT_SYS1, username="student", password=PASSWORD, timeout=10)
sftp = client.open_sftp()
sftp.get("group-chat-app/results.csv", "results.csv")

# Also download JSON files
for exp in ["1_backend", "3_backends"]:
    try:
        sftp.get(f"group-chat-app/{exp}.json", f"{exp}.json")
    except Exception:
        pass
sftp.close()
client.close()

print("\n" + "="*60)
print("=== FINAL RESULTS ===")
print("="*60)
with open("results.csv") as f:
    content = f.read()
    print(content)

print("\nDone!")
