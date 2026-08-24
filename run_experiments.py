import paramiko
import sys
import time

PASSWORD = "12342090"
HOST = "10.1.75.53"

def ssh_exec(client, command, timeout=120):
    print(f"  > {command}")
    stdin, stdout, stderr = client.exec_command(command, timeout=timeout)
    exit_status = stdout.channel.recv_exit_status()
    out = stdout.read().decode('utf-8', errors='replace')
    err = stderr.read().decode('utf-8', errors='replace')
    if out: print(out.strip())
    if err and "WARNING" not in err and "password" not in err:
        print(f"  STDERR: {err.strip()}")
    return exit_status, out

def check_backends():
    """Verify all 3 backends are up and healthy."""
    print("=" * 60)
    print("STEP 1: Checking backend health")
    print("=" * 60)
    ports = [(2238, "sys2"), (2239, "sys3"), (2240, "sys4")]
    all_ok = True
    for port, name in ports:
        client = paramiko.SSHClient()
        client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        client.connect(hostname=HOST, port=port, username="student", password=PASSWORD, timeout=10)
        status, out = ssh_exec(client, "curl -k -s https://localhost:5000/health")
        if '"status":"ok"' in out:
            print(f"  [OK] {name} (port {port}): HEALTHY")
        else:
            print(f"  [FAIL] {name} (port {port}): DOWN - restarting...")
            ssh_exec(client, "pkill -f server.py || true")
            time.sleep(1)
            ssh_exec(client, "export PORT=5000; nohup python3 group-chat-app/server/server.py > group-chat-app/server.log 2>&1 &")
            time.sleep(3)
            status, out = ssh_exec(client, "curl -k -s https://localhost:5000/health")
            if '"status":"ok"' in out:
                print(f"  [OK] {name} (port {port}): RECOVERED")
            else:
                print(f"  [FAIL] {name} (port {port}): STILL DOWN")
                all_ok = False
        client.close()
    return all_ok

def upload_and_run_experiments():
    """Upload updated files and run experiments on sys1."""
    print()
    print("=" * 60)
    print("STEP 2: Uploading updated files to sys1")
    print("=" * 60)
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(hostname=HOST, port=2237, username="student", password=PASSWORD, timeout=10)

    sftp = client.open_sftp()
    for local, remote in [
        ("load_balancer.go", "group-chat-app/load_balancer.go"),
        ("load_generator.go", "group-chat-app/load_generator.go"),
        ("run_experiments.sh", "group-chat-app/run_experiments.sh"),
    ]:
        print(f"  Uploading {local}...")
        sftp.put(local, remote)
    sftp.close()

    ssh_exec(client, "chmod +x group-chat-app/run_experiments.sh")

    print()
    print("=" * 60)
    print("STEP 3: Running experiments on sys1")
    print("=" * 60)
    status, out = ssh_exec(client, "cd group-chat-app && bash ./run_experiments.sh", timeout=300)

    print()
    print("=" * 60)
    print("STEP 4: Downloading results")
    print("=" * 60)
    sftp = client.open_sftp()
    try:
        sftp.get("group-chat-app/results.csv", "results.csv")
        print("  [OK] Downloaded results.csv")
    except Exception as e:
        print(f"  [FAIL] Failed to download results.csv: {e}")
    try:
        sftp.get("group-chat-app/1_backend.json", "1_backend.json")
        print("  [OK] Downloaded 1_backend.json")
    except Exception as e:
        print(f"  [FAIL] Failed: {e}")
    try:
        sftp.get("group-chat-app/3_backends.json", "3_backends.json")
        print("  [OK] Downloaded 3_backends.json")
    except Exception as e:
        print(f"  [FAIL] Failed: {e}")
    sftp.close()
    client.close()

if __name__ == "__main__":
    backends_ok = check_backends()
    if not backends_ok:
        print("\nWARNING: Some backends are down. Proceeding anyway...")
    upload_and_run_experiments()
    print("\n[OK] ALL DONE")
