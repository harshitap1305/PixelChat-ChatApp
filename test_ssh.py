import paramiko
import sys

def test_ssh(host, port, user, password):
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    try:
        print(f"Connecting to {host}:{port}...")
        client.connect(hostname=host, port=port, username=user, password=password, timeout=10)
        stdin, stdout, stderr = client.exec_command("go version")
        print(f"[{host}:{port}] go version:", stdout.read().decode().strip())
    except Exception as e:
        print(f"[{host}:{port}] Failed to connect: {e}")
    finally:
        client.close()

if __name__ == "__main__":
    systems = [
        ("10.1.75.53", 2237),
        ("10.1.75.53", 2238),
        ("10.1.75.53", 2239),
        ("10.1.75.53", 2240),
    ]
    for host, port in systems:
        test_ssh(host, port, "student", "12342090")
