#!/usr/bin/env python
# Copyright 2017 Marc-Antoine Ruel. All rights reserved.
# Use of this source code is governed under the Apache License, Version 2.0
# that can be found in the LICENSE file.

"""Find the Raspberry Pis on the network that broadcast via mDNS"""

import subprocess
import sys


def main():
  cmd = ['avahi-browse', '-t', '_workstation._tcp', '-r', '-p']
  out = subprocess.check_output(cmd, stderr=subprocess.STDOUT)
  # '=' eth IPv4 host Workstation local mDNS IP
  lines = [l.split(';') for l in out.splitlines() if l.startswith('=')]
  lines = [l for l in lines if l[2] == 'IPv4']
  print('\n'.join('%s: %s' % (i[6], i[7]) for i in lines))
  return 0


if __name__ == '__main__':
  sys.exit(main())
