#!/usr/bin/env python
# Copyright 2016 Marc-Antoine Ruel. All rights reserved.
# Use of this source code is governed under the Apache License, Version 2.0
# that can be found in the LICENSE file.

"""Fetches an image, flashes it to an SDCard and modifies it to bootstrap
automatically.
"""

import argparse
import glob
import os
import re
import shutil
import subprocess
import sys
import time
import zipfile


BASE = os.path.dirname(os.path.abspath(__file__))


# TODO(maruel): Figure the name automatically.
# This is where https://downloads.raspberrypi.org/raspbian_lite_latest redirects
# to.
RASPBIAN_JESSIE_LITE_URL = (
    'https://downloads.raspberrypi.org/raspbian_lite/images/'
    'raspbian_lite-2017-03-03/2017-03-02-raspbian-jessie-lite.zip')
RASPBIAN_JESSIE_LITE_IMG = '2017-03-02-raspbian-jessie-lite.img'


WPA_SUPPLICANT = """
network={
  ssid="%s"
  psk="%s"
}
"""


RASPBIAN_FIVE_INCHES_DISPLAY = """
# Enable support for 800x480 display:
hdmi_group=2
hdmi_mode=87
hdmi_cvt 800 480 60 6 0 0 0

# Enable touchscreen:
# Not necessary on Jessie Lite since it boots in console mode. :)
# Some displays use 22, others 25.
# Enabling this means the SPI bus cannot be used anymore.
#dtoverlay=ads7846,penirq=22,penirq_pull=2,speed=10000,xohms=150
"""


RC_LOCAL = """#!/bin/sh
# Copyright 2016 Marc-Antoine Ruel. All rights reserved.
# Use of this source code is governed under the Apache License, Version 2.0
# that can be found in the LICENSE file.

# Part of https://github.com/maruel/bin_pub

set -e

LOG_FILE=/var/log/firstboot.log
if [ ! -f $LOG_FILE ]; then
  /root/firstboot.sh 2>&1 | tee $LOG_FILE
fi
exit 0
"""


def check_call(*args):
  env = os.environ.copy()
  env['LANG'] = 'C'
  subprocess.check_call(args, env=env)


def check_output(*args):
  env = os.environ.copy()
  env['LANG'] = 'C'
  return subprocess.check_output(args, env=env, stderr=subprocess.STDOUT)


def mount(path):
  """Mounts a partition and returns the mount path."""
  print('- Mounting %s' % path)
  # "Mounted /dev/sdh2 at /media/<user>/<GUID>."
  r1 = re.compile('Mounted (?:[^ ]+) at ([^\\\\]+)\\.')
  # "Error mounting /dev/sdh2: GDBus.Error:org.freedesktop.UDisks2.Error.AlreadyMounted: Device /dev/sdh2"
  # "is already mounted at `/media/<user>/<GUID>'.
  r2 = re.compile('is already mounted at `([^\']+)\'')
  txt = check_output('/usr/bin/udisksctl', 'mount', '-b', path)
  # | sed 's/.\+ at \(.\+\)\+\./\1/')
  for r in (r1, r2):
    m = r.match(txt)
    if m:
      return m.group(1)
  assert False, txt


def umount(p):
  """Unmounts all the partitions on disk 'p'."""
  ret = True
  for i in sorted(glob.glob(p + '*')):
    if i != p:
      print('- Unmounting %s' % i)
      try:
        check_output('/usr/bin/udisksctl', 'unmount', '-f', '-b', i)
      except subprocess.CalledProcessError:
        print('  Unmounting failed')
        ret = False
  return ret


def fetch_img(args):
  """Fetches the distro image remotely."""
  if args.distro == 'raspbian':
    imgname = RASPBIAN_JESSIE_LITE_IMG
    if not os.path.isfile(imgname):
      zipname = 'raspbian_lite.zip'
      print('- Fetching Raspbian Jessie Lite latest')
      check_call('curl', '-L', '-o', zipname, RASPBIAN_JESSIE_LITE_URL)
      # Warning: look for the actual file, put it in a subdirectory.
      print('- Extracting zip')
      with zipfile.ZipFile(zipname) as f:
        f.extract(imgname)
      os.remove(zipname)
    return imgname
  # http://odroid.in/ubuntu_16.04lts/
  # https://www.armbian.com/download/
  # https://beagleboard.org/latest-images better to flash then run setup.sh
  # manually.
  # https://flash.getchip.com/ better to flash then run setup.sh manually.
  assert False, args.distro


def enable_5inches(args, root, boot):
  """Enable non-standard 5" 800x480 display support.

  Found one at 23$USD with free shipping on aliexpress.
  """
  if args.distro == 'raspbian':
    print('- Enabling 5\" display support')
    with open(os.path.join(os.path.join(boot, 'config.txt')), 'wb') as f:
      f.write(RASPBIAN_FIVE_INCHES_DISPLAY)
    return
  assert False, args.distro


def setup_first_boot(args, root, boot):
  print('- First boot setup script')
  shutil.copyfile('setup.sh', os.path.join(root, 'root/firstboot.sh'))
  os.chmod(os.path.join(root, 'root/firstboot.sh'), 0755)
  # Skip this step to debug firstboot.sh. Then login at the console and run the
  # script manually.
  rc_local = os.path.join(root, 'etc/rc.local')
  os.rename(rc_local, os.path.join(root, 'etc/rc.local.old'))
  with open(rc_local, 'wb') as f:
    f.write(RC_LOCAL)
  os.chmod(rc_local, 0755)


def setup_ssh(args, root, boot):
  print('- SSH keys')
  # This assumes you have properly set your own ssh keys and plan to use them.
  path = os.path.join(root, 'home/%s/.ssh' % args.user)
  if not os.path.isdir(path):
    os.makedirs(path)
  shutil.copyfile(
      args.ssh_key,
      os.path.join(root, 'home/%s/.ssh/authorized_keys' % args.user))
  # On all (?) distros, the first user is 1000. This is at least true on
  # Raspbian and NextThing's Debian distro.
  check_call(
      'chown', '-R', '1000:1000', os.path.join(root, 'home/%s/.ssh' % args.user))
  # Force key based authentication since the password is known.
  check_call(
      'sed', '-i', 's/#PasswordAuthentication yes/PasswordAuthentication no/',
       os.path.join(root, 'etc/ssh/sshd_config'))
  if args.distro == 'raspbian':
    # https://www.raspberrypi.org/documentation/remote-access/ssh/
    open(os.path.join(boot + 'ssh'), 'wb').close()


def setup_wifi(args, root, boot):
  print('- Wifi')
  with open(os.path.join(root, 'etc/wpa_supplicant/wpa_supplicant.conf'), 'wb') as f:
    f.write(WPA_SUPPLICANT % (args.wifi[0], args.wifi[1]))


def flash(args):
  """Flashes args.img to args.path."""
  print('- Unmounting')
  umount(args.path)
  print('- Flashing (takes 2 minutes)')
  check_call('dd', 'bs=4M', 'if=' + args.img, 'of=' + args.path)
  print('- Flushing I/O cache')
  # This is important otherwise the mount afterward may 'see' the old partition
  # table.
  check_call('sync')

  print('- Reloading partition table')
  # Wait a bit to try to workaround "Error looking up object for device" when
  # immediately using "/usr/bin/udisksctl mount" after this script.
  check_call('partprobe', args.path)
  check_call('sync')
  time.sleep(1)
  # Needs 'p' for /dev/mmcblkN but not for /dev/sdX
  path = (args.path + 'p' if 'mmcblk' in args.path else args.path) + '2'
  while True:
    try:
      os.stat(path)
      break
    except OSError:
      print(' (still waiting for partition %s to show up)' % path)
      time.sleep(1)


def as_root(args):
  if not args.skip_flash:
    flash(args)
  # Needs 'p' for /dev/mmcblkN but not for /dev/sdX
  path = args.path + 'p' if 'mmcblk' in args.path else args.path
  umount(args.path)
  boot = mount(path + '1')
  print('  /boot mounted as ' + boot)
  root = mount(path + '2')
  print('  / mounted as ' + root)

  setup_first_boot(args, root, boot)
  if args.five_inches:
    # TODO(maruel): Only for Raspbian.
    enable_5inches(args, root, boot)
  if args.ssh_key:
    setup_ssh(args, root, boot)
  if args.wifi:
    setup_wifi(args, root, boot)
  # https://www.raspberrypi.org/forums/viewtopic.php?f=28&t=141195
  # enable_uart=1 for RPi?

  print('- Unmounting')
  check_call('sync')
  umount(args.path)
  print('')
  print('You can now remove the SDCard safely and boot your Raspberry Pi')
  print('Then connect with:')
  print('  ssh -o StrictHostKeyChecking=no pi@raspberrypi')
  print('')
  print('You can follow the update process by either connecting a monitor')
  print('to the HDMI port or by ssh\'ing into the Pi and running:')
  print('  tail -f /var/log/firstboot.log')
  return 0


def find_public_key():
  for i in ('authorized_keys', 'id_ed25519.pub', 'id_ecdsa.pub', 'id_rsa.pub'):
    p = os.path.join(os.environ['HOME'], '.ssh', i)
    if os.path.isfile(p):
      return p


def main():
  # Make it usable without root with:
  # sudo setcap CAP_SYS_ADMIN,CAP_DAC_OVERRIDE=ep __file__
  os.chdir(BASE)
  parser = argparse.ArgumentParser(description=sys.modules[__name__].__doc__)
  parser.add_argument('--as-root', action='store_true', help=argparse.SUPPRESS)
  parser.add_argument('--img', help=argparse.SUPPRESS)
  parser.add_argument(
      '--ssh-key', default=find_public_key(),
      help='ssh public key to use, default: $(default)s')
  parser.add_argument(
      '--distro', choices=('raspbian', ),
      help='Select the distribution to install',
      required=True)
  parser.add_argument(
      '--wifi', metavar=('SSID', 'PWD'), nargs=2, help='wifi ssid and password')
  parser.add_argument(
      '--5inch', action='store_true', dest='five_inches',
      help='Enable support for 5" 800x480 display (raspbian only)')
  parser.add_argument(
      '--skip-flash', action='store_true',
      help='Skip download and flashing, just modify the image')
  parser.add_argument(
      'path',
      help='Path to SD card, generally in the form of /dev/sdX or /dev/mmcblkN')
  args = parser.parse_args()

  if args.distro == 'raspbian':
    args.user = 'pi'
  elif args.distro == 'chip':
    args.user = 'chip'
  else:
    args.user = 'debian'

  if args.as_root:
    return as_root(args)

  imgname = fetch_img(args)
  print('Warning! This will blow up everything in %s' % args.path)
  print('')
  print('This script has minimal use of \'sudo\' for \'dd\' and modifying the partitions')
  print('')
  cmd = [
    'sudo', sys.executable, '-s', '-S',
    os.path.join(BASE, os.path.basename(__file__)),
    '--as-root',
    '--distro', args.distro,
    '--ssh-key', args.ssh_key,
    '--img', imgname,
  ]
  # Propagate optional flags.
  if args.wifi:
    cmd.extend(('--wifi', args.wifi[0], args.wifi[1]))
  if args.five_inches:
    cmd.append('--5inch')
  if args.skip_flash:
    cmd.append('--skip-flash')
  cmd.append(args.path)
  return subprocess.call(cmd)


if __name__ == '__main__':
  sys.exit(main())
