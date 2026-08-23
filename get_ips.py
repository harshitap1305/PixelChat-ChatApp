import paramiko

def get_ip(port):
    client = paramiko.SSHClient()
    client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    client.connect(hostname="10.1.75.53", port=port, username="student", password="12342090")
    stdin, stdout, stderr = client.exec_command("hostname -I")
    ip = stdout.read().decode().strip().split()[0]
    client.close()
    return ip

print("sys1 IP:", get_ip(2237))
print("sys2 IP:", get_ip(2238))
print("sys3 IP:", get_ip(2239))
print("sys4 IP:", get_ip(2240))
