IMAP server
=================
Build an IMAP server using go-imap/v2/imapserver that:

- Authenticates users from a plain text file
- Stores emails as text files on the filesystem
- Only shows emails from whitelisted senders

File Structure
================
imapd/
├── main.go           # Server entry point and configuration
├── session.go        # Session interface implementation
├── storage.go        # Filesystem-based email storage
├── auth.go           # User authentication from text file
├── users.txt         # User credentials (username:password per line)
└── whitelist.txt     # Whitelisted sender addresses (one per line)

