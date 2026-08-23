import paramiko
import sys

client = paramiko.SSHClient()
client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
client.connect(hostname="10.1.75.53", port=2237, username="student", password="12342090")

print("Checking hosts file...")
stdin, stdout, stderr = client.exec_command("cat /etc/hosts")
print(stdout.read().decode())

print("Checking curl to sys2...")
stdin, stdout, stderr = client.exec_command("curl -v http://sys2:8081/health")
print(stderr.read().decode())

print("Checking curl to 10.1.75.53:8081...")
stdin, stdout, stderr = client.exec_command("curl -v http://10.1.75.53:8081/health")
print(stderr.read().decode())

print("Checking curl to 10.1.75.53:8000...")
stdin, stdout, stderr = client.exec_command("curl -v http://10.1.75.53:8000/health")
print(stderr.read().decode())

client.close()
