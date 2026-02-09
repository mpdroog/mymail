#!/bin/bash
set -euo pipefail
IFS=$'\n\t'

VERBOSE=false
FLAGS=""

while getopts ":v" opt; do
  case $opt in
    v)
    VERBOSE=true
    set -x
    ;;
  \?)
      echo "Invalid option: -$OPTARG" >&2
      exit 1
      ;;
    :)
      echo "Option -$OPTARG requires an argument." >&2
      exit 1
      ;;
  esac
done

if [ "$VERBOSE" = true ] ; then
  FLAGS="-v"
fi

cd imapd
env GOOS=linux GOARCH=amd64 go build
echo -n $'\003' | dd bs=1 count=1 seek=7 conv=notrunc of=./imapd
cd -

cd smtpd
env GOOS=linux GOARCH=amd64 go build
echo -n $'\003' | dd bs=1 count=1 seek=7 conv=notrunc of=./smtpd
cd -

scp smtpd/smtpd imapd/imapd ams1:~
ssh ams1 '/etc/mymail/reload.sh'