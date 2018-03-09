# mesos-ssh
This tool runs commands on some or all nodes of a Mesos cluster via ssh.
It can optionally upload files or scripts to accompany those commands,
making it a good tool for ad-hoc maintenance.

## Usage
```
Usage: ./mesos-ssh [OPTIONS] <masters|public|private|agents|all> <cmd>
  -debug
        Write debug output
  -f value
        Send specified file to a temporary directory before running the command.
        The command will be invoked from inside the temporary directory, and the
        directory will be deleted after execution is completed.  This can be
        specified multiple times.
  -forward-agent
        Forwards the local SSH agent to the remote host
  -interleave
        Interleave output from each session rather than wait for it to finish
  -key string
        Use the specified keyfile to authenticate to the remote host
  -m int
        How many sessions to run in parallel (default 4)
  -mesos string
        Address of Mesos leader (default "http://leader.mesos:5050")
  -no-agent
        Do not use the local ssh agent to authenticate remotely
  -passfile string
        Use the contents of the specified file as the SSH password
  -port int
        SSH port (default 22)
  -pty
        Run command in a pty (automatically applied with -sudo)
  -sudo
        Run commands as superuser on the remote machine
  -timeout duration
        Timeout for remote command (default 1m0s)
  -user string
        Remote username (default "jj")
```

### Remote hosts
The first argument after any options is what machines to run the command on. 
The options are:

* `all`: All masters and agents.
* `agents`: All agents.
* `masters`: All masters.
* `public`: Agents with a role called `slave_public`
* `private`: Agents without a role called `slave_public`.
* `<file>`: Connect to IP addresses listed in this file.

`mesos-ssh` finds masters via a DNS lookup on `master.mesos`, and finds
agents by querying the Mesos REST API.

### Authentication
By default, the current user name is used as the user on the remote machine. 
This can overridden by `-user`.

If a local SSH agent is found, then it will be used for authentication
unless `-no-agent` is specified.  Passwords are only prompted if neither the
agent nor any specified private key is accepted for authentication. 
Passwords may also be prompted when `-sudo` is specified and any machine
brings up a sudo password prompt.

### `sudo`
Commands can be run as administrator if `-sudo` is specified.  The sudo
password prompt will be answered with a password in this case.  One thing to
note is that `-sudo` will force `-pty` (because sudo will not prompt for a
password if a terminal is not allocated).  This may cause undesirable
behavior such as applications using pagers to display results, or things
like `apt-get` prompting for input.

### Files
When `-f` is specified, a temporary directory is created on each remote
host, where all files will be uploaded.  `cmd` is then invoked from within
that directory.  Finally, the directory is removed prior to disconnection. 
File modes are preserved upon transfer.

### Output
By default, all the output for each connection will be displayed once the
command has run and the connection has closed.  For longer-running scripts
with more output, it might be desirable to see output as it arrives.  This
can be enabled with the `-interleaved` option.

## Examples
```sh
% mesos-ssh all uptime
% mesos-ssh -f installer.dpkg -sudo -interleaved all 'dpkg -i installer.dpkg || apt-get install -f -y'
```

## License

This is MIT-licensed.
