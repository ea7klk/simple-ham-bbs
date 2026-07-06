import socket

# Configuration
SERVER = "rotate.aprs2.net"
PORT = 14580
CALLSIGN = "EA7KLK"
PASSCODE = "19875"
# 't/m' filters for text messages only
LOGIN_LINE = f"user {CALLSIGN} pass {PASSCODE} vers PyClient 1.0 filter b/EA7KLK t/b/n\r\n"

def connect_aprs():
    # Create TCP socket
    s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    
    try:
        print(f"Connecting to {SERVER}:{PORT}...")
        s.connect((SERVER, PORT))
        
        # Send authentication and filter
        s.sendall(LOGIN_LINE.encode('utf-8'))
        print("Authenticated successfully. Listening for live messages...\n")
        
        # Continuous stream loop
        buffer = ""
        while True:
            data = s.recv(4096).decode('utf-8', errors='ignore')
            if not data:
                print("Connection lost.")
                break
                
            buffer += data
            while "\n" in buffer:
                line, buffer = buffer.split("\n", 1)
                line = line.strip()
                
                # Ignore server comments
                if line.startswith("#"):
                    continue
                    
                print(line)
                
    except KeyboardInterrupt:
        print("\nDisconnecting from APRS-IS.")
    finally:
        s.close()

if __name__ == "__main__":
    connect_aprs()
