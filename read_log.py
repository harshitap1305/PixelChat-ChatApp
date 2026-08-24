import paramiko
client = paramiko.SSHClient()
client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
client.connect(hostname="10.1.75.53", port=2238, username="student", password="12342090", timeout=10)
stdin, stdout, stderr = client.exec_command("cat group-chat-app/server.log")
print(stdout.read().decode())
