#!/usr/bin/env python3
"""Write boot-live's fixture trees (testdata/<mac>/): /sys, /proc, /dev and /etc files as the
live system shows them, from the real probe data of the three Macs (omaboot-spikes results,
omacdiag-runs macinfo) plus reconstructed parts where the spikes recorded none."""
import os, sys, shutil

OUT = sys.argv[1]


def w(root, path, text):
    p = os.path.join(root, path.lstrip('/'))
    os.makedirs(os.path.dirname(p), exist_ok=True)
    with open(p, 'w') as f:
        f.write(text if text.endswith('\n') or text == '' else text + '\n')


def ln(root, path, target):
    p = os.path.join(root, path.lstrip('/'))
    os.makedirs(os.path.dirname(p), exist_ok=True)
    if os.path.lexists(p):
        os.remove(p)
    os.symlink(target, p)


def pci(root, slot, cls, vid, sub, driver=None, boot_vga=None):
    d = f'/sys/bus/pci/devices/{slot}'
    v, dv = vid.split(':')
    sv, sd = sub.split(':') if sub else ('0000', '0000')
    w(root, d + '/vendor', '0x' + v)
    w(root, d + '/device', '0x' + dv)
    w(root, d + '/subsystem_vendor', '0x' + sv)
    w(root, d + '/subsystem_device', '0x' + sd)
    w(root, d + '/class', '0x' + cls)
    ue = []
    if driver:
        ue.append('DRIVER=' + driver)
    ue += [f'PCI_CLASS={int(cls, 16):X}', f'PCI_ID={v.upper()}:{dv.upper()}', f'PCI_SUBSYS_ID={sv.upper()}:{sd.upper()}', f'PCI_SLOT_NAME={slot}']
    w(root, d + '/uevent', '\n'.join(ue))
    if boot_vga is not None:
        w(root, d + '/boot_vga', '1' if boot_vga else '0')
    w(root, d + '/power/runtime_status', 'active')


def usb(root, name, vid, storage=False, hub=False):
    d = f'/sys/bus/usb/devices/{name}'
    v, p = vid.split(':')
    w(root, d + '/idVendor', v)
    w(root, d + '/idProduct', p)
    w(root, d + '/bDeviceClass', '09' if hub else '00')
    if storage:
        w(root, d + f'/{name}:1.0/bInterfaceClass', '08')


def drm(root, card, slot, uevent_pci, connectors, render='renderD128'):
    """connectors: list of (name, status, modes)"""
    w(root, f'/sys/class/drm/{card}/device/uevent', uevent_pci)
    if render:
        w(root, f'/sys/class/drm/{render}/device/uevent', uevent_pci)
        w(root, f'/dev/dri/{render}', '')
    for name, status, modes in connectors:
        c = f'/sys/class/drm/{card}-{name}'
        w(root, c + '/status', status)
        w(root, c + '/enabled', 'enabled' if status == 'connected' else 'disabled')
        w(root, c + '/modes', '\n'.join(modes))


def pci_uevent(root, slot):
    with open(os.path.join(root, f'sys/bus/pci/devices/{slot}/uevent')) as f:
        return f.read()


def block(root, name, devpath, parts, removable=0, size=1000215216, ro=0):
    target = f'../devices/{devpath}/block/{name}'
    ln(root, f'/sys/block/{name}', target)
    real = f'/sys/devices/{devpath}/block/{name}'
    w(root, real + '/ro', str(ro))
    w(root, real + '/removable', str(removable))
    w(root, real + '/size', str(size))
    w(root, real + '/uevent', f'DEVTYPE=disk\nDEVNAME={name}')
    for i, pn in enumerate(parts, 1):
        w(root, real + f'/{pn}/partition', str(i))
        w(root, real + f'/{pn}/ro', str(ro))
        w(root, real + f'/{pn}/uevent', f'DEVTYPE=partition\nDEVNAME={pn}\nPARTN={i}')


def hwmon(root, n, name, temps, device=None):
    d = f'/sys/class/hwmon/hwmon{n}'
    w(root, d + '/name', name)
    for i, (label, inp, crit) in enumerate(temps, 1):
        if label:
            w(root, d + f'/temp{i}_label', label)
        w(root, d + f'/temp{i}_input', str(inp))
        if crit:
            w(root, d + f'/temp{i}_crit', str(crit))


def kmsg(root, lines):
    out = []
    for i, (t, lvl, msg) in enumerate(lines):
        out.append(f'{lvl},{i + 1},{int(t * 1e6)},-;{msg}')
    w(root, '/dev/kmsg', '\n'.join(out))


def common(root, kver, cmdline, live=True):
    w(root, '/proc/sys/kernel/osrelease', kver)
    w(root, '/proc/cmdline', cmdline)
    w(root, '/sys/firmware/efi/fw_platform_size', '64')
    w(root, '/etc/os-release', 'NAME="Arch Linux"\nPRETTY_NAME="Arch Linux"\nID=arch\nBUILD_ID=rolling\n')
    if live:
        w(root, '/run/archiso/bootmnt/.keep', '')
        w(root, '/usr/share/omaboot/live-image', '')
        w(root, '/usr/share/omaboot/build', 'live 2026-10-10')
        w(root, '/usr/share/omaboot/omarchy-version', '4.0.4')
        w(root, '/usr/share/omaboot/modules.txt', f'kernel={kver}\n')
    w(root, '/proc/mounts', '\n'.join([
        'airootfs / overlay rw,relatime,lowerdir=/run/archiso/sfs/airootfs 0 0',
        'proc /proc proc rw,nosuid,nodev,noexec,relatime 0 0',
        'sysfs /sys sysfs rw,nosuid,nodev,noexec,relatime 0 0',
        '/dev/sdb1 /run/archiso/bootmnt iso9660 ro,relatime 0 0',
        '/dev/loop0 /run/archiso/airootfs squashfs ro,relatime 0 0',
    ]))
    w(root, '/sys/class/graphics/fb0/.keep', '')


def lo_net(root):
    w(root, '/sys/class/net/lo/type', '772')
    w(root, '/sys/class/net/lo/operstate', 'unknown')


def netdev(root, name, driver, slot=None, wifi=False, carrier=None, oper='down', bus='pci'):
    d = f'/sys/class/net/{name}'
    w(root, d + '/type', '1')
    w(root, d + '/operstate', oper)
    w(root, d + '/uevent', f'INTERFACE={name}' + ('\nDEVTYPE=wlan' if wifi else ''))
    if carrier is not None:
        w(root, d + '/carrier', '1' if carrier else '0')
    if wifi:
        w(root, d + '/phy80211/name', 'phy0')
        w(root, d + '/wireless/.keep', '')
    ue = [f'DRIVER={driver}']
    if slot:
        ue += [f'PCI_SLOT_NAME={slot}']
    w(root, d + '/device/uevent', '\n'.join(ue))


def mac_air(root):
    """MacBookAir5,2: PCI and USB from omacdiag-runs/2026-10-03-01 macinfo (lspci -nnk, lsusb),
    without the Thunderbolt Ethernet adapter that was plugged in then; DRM, EDID mode, framebuffer,
    applesmc (fan, light, keyboard backlight) from omaboot-spikes. Battery values are made up."""
    kver = '7.2.9-arch1-Watanare-T2-1-t2'
    common(root, kver, 'BOOT_IMAGE=/arch/boot/x86_64/vmlinuz-linux-t2 archisobasedir=arch archisosearchuuid=2026-10-10-03-12-40-00 quiet splash')
    dmi = '/sys/class/dmi/id/'
    for k, v in {'product_name': 'MacBookAir5,2', 'board_name': 'Mac-2E6FAB96566FE58C', 'board_vendor': 'Apple Inc.',
                 'sys_vendor': 'Apple Inc.', 'product_family': 'MacBook Air', 'bios_version': '429.0.0.0.0', 'chassis_type': '10',
                 'product_serial': 'C02TESTSERIAL', 'board_serial': 'C02TESTBOARDSERIAL'}.items():
        w(root, dmi + k, v)
    w(root, '/proc/cpuinfo', '\n'.join(f'processor\t: {i}\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Core(TM) i5-3427U CPU @ 1.80GHz\n' for i in range(4)))
    w(root, '/proc/meminfo', 'MemTotal:        3903976 kB\nMemFree:         2903976 kB')
    devs = [
        ('0000:00:00.0', '060000', '8086:0154', '106b:00fe', 'ivb_uncore'),
        ('0000:00:02.0', '030000', '8086:0166', '106b:00fe', 'i915'),
        ('0000:00:14.0', '0c0330', '8086:1e31', '8086:7270', 'xhci_hcd'),
        ('0000:00:16.0', '078000', '8086:1e3a', '8086:7270', 'mei_me'),
        ('0000:00:1a.0', '0c0320', '8086:1e2d', '8086:7270', 'ehci-pci'),
        ('0000:00:1b.0', '040300', '8086:1e20', '8086:7270', 'snd_hda_intel'),
        ('0000:00:1c.0', '060400', '8086:1e10', '8086:7270', 'pcieport'),
        ('0000:00:1c.1', '060400', '8086:1e12', '8086:7270', 'pcieport'),
        ('0000:00:1c.4', '060400', '8086:1e18', '8086:7270', 'pcieport'),
        ('0000:00:1d.0', '0c0320', '8086:1e26', '8086:7270', 'ehci-pci'),
        ('0000:00:1f.0', '060100', '8086:1e56', '8086:7270', 'lpc_ich'),
        ('0000:00:1f.2', '010601', '8086:1e03', '8086:7270', 'ahci'),
        ('0000:00:1f.3', '0c0500', '8086:1e22', '8086:7270', 'i801_smbus'),
        ('0000:02:00.0', '028000', '14e4:4353', '106b:00e9', 'bcma-pci-bridge'),
        ('0000:03:00.0', '060400', '8086:1547', '2222:1111', 'pcieport'),
        ('0000:04:00.0', '060400', '8086:1547', '2222:1111', 'pcieport'),
        ('0000:04:03.0', '060400', '8086:1547', '2222:1111', 'pcieport'),
        ('0000:04:04.0', '060400', '8086:1547', '2222:1111', 'pcieport'),
        ('0000:04:05.0', '060400', '8086:1547', '2222:1111', 'pcieport'),
        ('0000:04:06.0', '060400', '8086:1547', '2222:1111', 'pcieport'),
        ('0000:05:00.0', '088000', '8086:1547', '2222:1111', 'thunderbolt'),
    ]
    for slot, cls, vid, sub, drv in devs:
        pci(root, slot, cls, vid, sub, drv, boot_vga=(True if slot == '0000:00:02.0' else None))
    for name, vid, hub in [('usb1', '1d6b:0002', True), ('usb2', '1d6b:0002', True), ('2-1', '8087:0024', True),
                           ('2-1.1', '0424:2513', True), ('2-1.1.1', '0a5c:4500', True), ('2-1.2', '05ac:024c', False),
                           ('2-1.3', '05ac:8404', False), ('2-1.1.1.1', '05ac:821f', False), ('usb3', '1d6b:0003', True),
                           ('usb4', '1d6b:0002', True), ('4-1', '8087:0024', True), ('4-1.1', '05ac:8510', False)]:
        usb(root, name, vid, hub=hub)
    usb(root, '1-1', '0951:1643', storage=True)  # the boot stick (Kingston DataTraveler G3)
    usb(root, '1-2', '0781:5583', storage=True)  # a BOOTLIVE stick
    drm(root, 'card1', '0000:00:02.0', pci_uevent(root, '0000:00:02.0'),
        [('DP-1', 'disconnected', []), ('DP-2', 'disconnected', []), ('HDMI-A-1', 'disconnected', []),
         ('HDMI-A-2', 'disconnected', []), ('VGA-1', 'disconnected', []), ('eDP-1', 'connected', ['1440x900'])])
    w(root, '/sys/class/graphics/fb0/name', 'i915drmfb')
    w(root, '/sys/class/graphics/fb0/virtual_size', '1440,900')
    w(root, '/sys/class/backlight/acpi_video0/max_brightness', '15')
    # the hands-on tests' files (reconstructed): the backlight's level, the lid switch, the
    # RTC alarm and the sleep states
    w(root, '/sys/class/backlight/acpi_video0/brightness', '11')
    w(root, '/sys/class/backlight/acpi_video0/actual_brightness', '11')
    w(root, '/sys/class/backlight/acpi_video0/type', 'firmware')
    w(root, '/sys/class/input/event0/device/name', 'Lid Switch')
    w(root, '/sys/class/input/event0/device/capabilities/ev', '21')
    w(root, '/sys/class/input/event0/device/capabilities/sw', '1')
    w(root, '/proc/acpi/button/lid/LID0/state', 'state:      open')
    w(root, '/sys/class/rtc/rtc0/wakealarm', '')
    w(root, '/sys/power/state', 'freeze mem')
    w(root, '/sys/power/mem_sleep', 's2idle [deep]')
    b = '/sys/class/power_supply/BAT0/'
    for k, v in {'type': 'Battery', 'present': '1', 'status': 'Discharging', 'capacity': '87', 'energy_now': '30450000',
                 'energy_full': '35000000', 'energy_full_design': '50000000', 'cycle_count': '412', 'manufacturer': 'SMP', 'model_name': 'bq20z451'}.items():
        w(root, b + k, v)
    w(root, '/sys/class/power_supply/ADP1/type', 'Mains')
    w(root, '/sys/class/power_supply/ADP1/online', '0')
    hwmon(root, 0, 'ACAD', [])
    hwmon(root, 1, 'coretemp', [('Package id 0', 41000, 105000), ('Core 0', 40000, 105000), ('Core 1', 41000, 105000)])
    smc = '/sys/bus/acpi/devices/APP0001:00/'
    for k, v in {'fan1_label': 'Exhaust', 'fan1_input': '2004', 'fan1_min': '2000', 'fan1_max': '6500', 'light': '(8,0)'}.items():
        w(root, smc + k, v)
    w(root, '/sys/class/leds/smc::kbd_backlight/max_brightness', '255')
    w(root, '/sys/class/leds/smc::kbd_backlight/brightness', '0')
    w(root, '/sys/module/applesmc/refcnt', '0')
    w(root, f'/usr/lib/modules/{kver}/modules.dep', '\n'.join([
        'kernel/drivers/gpu/drm/i915/i915.ko.zst: kernel/drivers/gpu/drm/display/drm_display_helper.ko.zst',
        'updates/applesmc.ko.zst:',
        'updates/radeon.ko.zst: kernel/drivers/gpu/drm/ttm/ttm.ko.zst',
    ]))
    w(root, '/proc/asound/cards', ' 0 [PCH            ]: HDA-Intel - HDA Intel PCH\n                      HDA Intel PCH at 0xa0610000 irq 52')
    w(root, '/proc/asound/card0/codec#0', 'Codec: Cirrus Logic CS4206\nAddress: 0\nVendor Id: 0x10134206')
    w(root, '/proc/asound/card0/codec#3', 'Codec: Intel PantherPoint HDMI\nAddress: 3\nVendor Id: 0x80862806')
    lo_net(root)
    netdev(root, 'wlan0', 'brcmsmac', wifi=True, oper='down', bus='bcma')
    block(root, 'sda', 'pci0000:00/0000:00:1f.2/ata1/host0/target0:0:0/0:0:0:0', ['sda1', 'sda2', 'sda3'])
    block(root, 'sdb', 'pci0000:00/0000:00:1a.0/usb1/1-1/1-1:1.0/host6/target6:0:0/6:0:0:0', ['sdb1', 'sdb2'], removable=1, size=31277232)
    block(root, 'sdc', 'pci0000:00/0000:00:1a.0/usb1/1-2/1-2:1.0/host7/target7:0:0/7:0:0:0', ['sdc1'], removable=1, size=60062500)
    ln(root, '/sys/block/loop0', '../devices/virtual/block/loop0')
    w(root, '/sys/devices/virtual/block/loop0/ro', '1')
    ln(root, '/dev/disk/by-label/BOOTLIVE', '../../sdc1')
    kmsg(root, [
        (0.12, 3, 'DMAR-IR: [Firmware Bug]: ioapic 2 has no mapping iommu, interrupt remapping will be disabled'),
        (3.12, 6, 'i915 0000:00:02.0: [drm] Found ivybridge (device ID 0166) integrated display version 7.00 stepping N/A'),
        (3.25, 6, '[drm] Initialized i915 1.6.0 for 0000:00:02.0 on minor 1'),
        (9.10, 6, 'applesmc: key=424 fan=1 temp=31 index=30 acc=0 lux=2 kbd=1'),
        (12.59, 6, 'snd_hda_intel 0000:00:1b.0: bound 0000:00:02.0 (ops intel_audio_component_bind_ops [i915])'),
    ])
    egl(root, [('/dev/dri/renderD128', 'Mesa Intel(R) HD Graphics 4000 (IVB GT2)', 'OpenGL ES 3.0 Mesa 26.2.2-arch1.1', 'Intel', 206.4, 188.3)])
    vblank(root, 'card1', 'eDP-1', '1440x900', 60.0, 60, 1.0)
    w(root, '/iwd-networks.txt', '\n'.join(['maple-house|-52|psk', 'maple-house-5G|-55|psk', 'TELUS2381|-67|psk', 'BELL442|-74|psk', 'DIRECT-4F-HP M227|-81|psk', 'Cafe-Guest|-83|open']))


def egl(root, nodes):
    """The GPU helper's output (reconstructed for the slowest second: the spikes timed one second)."""
    out = []
    for node, renderer, version, vendor, fps, min_fps in nodes:
        out += [f'node={node}', 'ok=1', 'gbm_backend=drm', 'egl=1.5', 'egl_vendor=Mesa Project', 'api=gles3',
                f'vendor={vendor}', f'renderer={renderer}', f'version={version}', 'llvmpipe=0', 'draw_ok=1',
                'draw_pixel=64,128,191,255', f'bench_fps={fps}', f'bench_min_fps={min_fps}',
                'bench_windows=10', 'bench_s=10.0', 'end=1']
    w(root, '/egl-probe.txt', '\n'.join(out))


def vblank(root, card, conn, mode, hz, n, secs):
    """The internal panel's vblanks as the graphics check counts them (reconstructed)."""
    w(root, '/drm-vblank.txt', '\n'.join([f'card={card}', f'connector={conn}', 'crtc=0', f'mode={mode}',
                                          f'mode_hz={hz}', f'vblanks={n}', f'secs={secs}']))


def mac_imac10(root):
    """iMac10,1 (21.5-inch, Late 2009, Radeon HD 4670): DRM, panel modes, the radeon fix's kernel
    line and applesmc (3 fans, light) from omaboot-spikes (spike-look, spike-modules); booted on
    the stand-alone image's default entry (linux-omarchy), where applesmc is Omarchy's own. The
    chipset's PCI functions (NVIDIA MCP79) are reconstructed from the catalog and kernel log."""
    kver = '7.2.5-3-omarchy'
    common(root, kver, 'BOOT_IMAGE=/arch/boot/x86_64/vmlinuz-linux-omarchy archisobasedir=arch archisosearchuuid=2026-10-10-03-12-40-00 copytoram=n quiet splash')
    dmi = '/sys/class/dmi/id/'
    for k, v in {'product_name': 'iMac10,1', 'board_name': 'Mac-F2268CC8', 'board_vendor': 'Apple Inc.', 'sys_vendor': 'Apple Inc.',
                 'product_family': 'iMac', 'bios_version': '215.0.0.0.0', 'chassis_type': '13', 'product_serial': 'W80TESTSERIAL'}.items():
        w(root, dmi + k, v)
    w(root, '/proc/cpuinfo', '\n'.join(f'processor\t: {i}\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Core(TM)2 Duo CPU     E7600  @ 3.06GHz\n' for i in range(2)))
    w(root, '/proc/meminfo', 'MemTotal:       12234752 kB')
    devs = [
        ('0000:00:00.0', '060000', '10de:0a82', '106b:00b6', None),
        ('0000:00:03.0', '060100', '10de:0aae', '106b:00b6', None),
        ('0000:00:03.2', '0c0500', '10de:0aa2', '106b:00b6', 'nForce2_smbus'),
        ('0000:00:04.0', '0c0310', '10de:0aa5', '106b:00b6', 'ohci-pci'),
        ('0000:00:04.1', '0c0320', '10de:0aa6', '106b:00b6', 'ehci-pci'),
        ('0000:00:06.0', '0c0310', '10de:0aa7', '106b:00b6', 'ohci-pci'),
        ('0000:00:06.1', '0c0320', '10de:0aa9', '106b:00b6', 'ehci-pci'),
        ('0000:00:08.0', '040300', '10de:0ac0', '106b:00b6', 'snd_hda_intel'),
        ('0000:00:0b.0', '010601', '10de:0ab9', '106b:00b6', 'ahci'),
        ('0000:00:0a.0', '020000', '10de:0ab0', '106b:00b6', 'forcedeth'),
        ('0000:00:15.0', '060400', '10de:0ac6', '0000:0000', 'pcieport'),
        ('0000:02:00.0', '030000', '1002:9488', '106b:00b6', 'radeon'),
        ('0000:02:00.1', '040300', '1002:aa38', '106b:aa38', 'snd_hda_intel'),
        ('0000:03:00.0', '028000', '168c:002a', '106b:008f', 'ath9k'),
        ('0000:04:00.0', '0c0010', '104c:823f', '106b:00b6', 'firewire_ohci'),
    ]
    for slot, cls, vid, sub, drv in devs:
        pci(root, slot, cls, vid, sub, drv, boot_vga=(True if slot == '0000:02:00.0' else None))
    for name, vid, hub in [('usb1', '1d6b:0001', True), ('usb2', '1d6b:0002', True), ('usb3', '1d6b:0001', True), ('usb4', '1d6b:0002', True),
                           ('2-1', '05ac:8215', False), ('4-2', '05ac:8502', False), ('3-1', '05ac:8242', False), ('4-3', '05ac:8403', False)]:
        usb(root, name, vid, hub=hub)
    usb(root, '2-4', '0951:1643', storage=True)
    drm(root, 'card0', '0000:02:00.0', pci_uevent(root, '0000:02:00.0'),
        [('DP-1', 'disconnected', []), ('DP-2', 'disconnected', []),
         ('LVDS-1', 'connected', ['1920x1080', '1680x1050', '1400x1050', '1280x1024', '1440x900', '1280x960']), ('VGA-1', 'disconnected', [])])
    w(root, '/sys/class/graphics/fb0/name', 'radeondrmfb')
    w(root, '/sys/class/graphics/fb0/virtual_size', '1920,1080')
    hwmon(root, 0, 'coretemp', [(None, 0, 0), ('Core 0', 38000, 100000), ('Core 1', 37000, 100000)])
    os.remove(os.path.join(root, 'sys/class/hwmon/hwmon0/temp1_input'))
    hwmon(root, 1, 'radeon', [(None, 54000, 120000)])
    smc = '/sys/bus/acpi/devices/APP0001:00/'
    for k, v in {'fan1_label': 'ODD', 'fan1_input': '998', 'fan1_min': '1000', 'fan1_max': '4350',
                 'fan2_label': 'HDD', 'fan2_input': '2690', 'fan2_min': '1100', 'fan2_max': '6300',
                 'fan3_label': 'CPU', 'fan3_input': '1199', 'fan3_min': '1200', 'fan3_max': '4000', 'light': '(18,0)'}.items():
        w(root, smc + k, v)
    w(root, '/sys/module/applesmc/refcnt', '0')
    w(root, f'/usr/lib/modules/{kver}/modules.dep', '\n'.join([
        'kernel/drivers/hwmon/applesmc.ko.zst:',
        'updates/radeon.ko.zst: kernel/drivers/gpu/drm/ttm/ttm.ko.zst',
    ]))
    w(root, '/proc/asound/cards', ' 0 [NVidia         ]: HDA-Intel - HDA NVidia\n 1 [HDMI           ]: HDA-Intel - HD-Audio Generic')
    w(root, '/proc/asound/card0/codec#0', 'Codec: Cirrus Logic CS4206\nAddress: 0')
    w(root, '/proc/asound/card1/codec#0', 'Codec: ATI R6xx HDMI\nAddress: 0')
    lo_net(root)
    netdev(root, 'eth0', 'forcedeth', '0000:00:0a.0', carrier=True, oper='up')
    netdev(root, 'wlan0', 'ath9k', '0000:03:00.0', wifi=True)
    w(root, '/proc/net/route', 'Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\neth0\t00000000\t0101A8C0\t0003\t0\t0\t1024\t00000000\t0\t0\t0\neth0\t0001A8C0\t00000000\t0001\t0\t0\t1024\t00FFFFFF\t0\t0\t0')
    block(root, 'sda', 'pci0000:00/0000:00:0b.0/ata1/host0/target0:0:0/0:0:0:0', ['sda1', 'sda2'])
    block(root, 'sdb', 'pci0000:00/0000:00:04.1/usb2/2-4/2-4:1.0/host6/target6:0:0/6:0:0:0', ['sdb1', 'sdb2'], removable=1, size=31277232)
    block(root, 'sr0', 'pci0000:00/0000:00:0b.0/ata2/host1/target1:0:0/1:0:0:0', [], removable=1, size=0)
    # the SuperDrive (slot-loading) and the FireWire 800 bus's own node (reconstructed, 0.7.3)
    sr = '/sys/devices/pci0000:00/0000:00:0b.0/ata2/host1/target1:0:0/1:0:0:0/'
    for k, v in {'vendor': 'HL-DT-ST', 'model': 'DVDRW  GA11N', 'type': '5'}.items():
        w(root, sr + k, v)
    ln(root, '/sys/block/sr0/device', '../../../1:0:0:0')
    ln(root, '/sys/class/scsi_device/1:0:0:0/device', '../../../devices/pci0000:00/0000:00:0b.0/ata2/host1/target1:0:0/1:0:0:0')
    fw = '/sys/devices/pci0000:00/0000:00:15.0/0000:04:00.0/fw0/'
    for k, v in {'is_local': '1', 'guid': '0x0026b0fffe4bd8a2', 'vendor': '0x000a27', 'vendor_name': 'Apple Computer, Inc.',
                 'model': '0x000000', 'hardware_version': '0x000000'}.items():
        w(root, fw + k, v)
    ln(root, '/sys/bus/firewire/devices/fw0', '../../../devices/pci0000:00/0000:00:15.0/0000:04:00.0/fw0')
    kmsg(root, [
        (0.58, 6, 'pci 0000:02:00.0: vgaarb: setting as boot VGA device'),
        (3.40, 6, 'firewire_ohci 0000:04:00.0: added OHCI v1.10 device as card 0, 4 IR + 8 IT contexts, quirks 0x2'),
        (4.10, 6, '[drm] radeon kernel modesetting enabled.'),
        (4.96, 6, 'radeon 0000:02:00.0: [drm]   LVDS-1'),
        (5.90, 6, '[drm] iMac LVDS test: ATOM panel clock 108000 -> 138500 kHz, RGB888/FPDI enabled'),
        (6.20, 6, '[drm] Initialized radeon 2.51.0 for 0000:02:00.0 on minor 0'),
        (19.75, 6, 'input: HDA NVidia Headphone as /devices/pci0000:00/0000:00:08.0/sound/card0/input11'),
    ])
    egl(root, [('/dev/dri/renderD128', 'AMD RV730 (DRM 2.51.0 / 7.2.5-3-omarchy, LLVM 22.1.8)', 'OpenGL ES 3.0 Mesa 26.2.2-arch1.1', 'Mesa', 464.0, 431.5)])
    vblank(root, 'card0', 'LVDS-1', '1920x1080', 59.9, 60, 1.002)


def mac_imac18(root):
    """iMac18,3 (Retina 5K, 27-inch, 2017): DRM, EDID modes, framebuffer and applesmc from
    omaboot-spikes; booted on linux-omarchy (no T2 chip). Other PCI functions are reconstructed
    from the catalog (Wi-Fi, Ethernet, card reader, Thunderbolt, storage) and Kaby Lake chipset
    IDs; the NVMe SSD is the catalog's build-to-order one. Its chassis type is 9 (laptop), as the
    real one reports (boot-live 0.3.3), and a USB Magic Keyboard's battery is a power supply
    (scope Device) whose name carries the keyboard's serial number (a made-up one here)."""
    kver = '7.2.5-3-omarchy'
    common(root, kver, 'BOOT_IMAGE=/arch/boot/x86_64/vmlinuz-linux-omarchy archisobasedir=arch archisosearchuuid=2026-10-10-03-12-40-00 quiet splash')
    dmi = '/sys/class/dmi/id/'
    for k, v in {'product_name': 'iMac18,3', 'board_name': 'Mac-BE088AF8C5EB4FA2', 'board_vendor': 'Apple Inc.', 'sys_vendor': 'Apple Inc.',
                 'product_family': 'iMac', 'bios_version': '529.140.2.0.0', 'chassis_type': '9'}.items():
        w(root, dmi + k, v)
    w(root, '/proc/cpuinfo', '\n'.join(f'processor\t: {i}\nvendor_id\t: GenuineIntel\nmodel name\t: Intel(R) Core(TM) i5-7500 CPU @ 3.40GHz\n' for i in range(4)))
    w(root, '/proc/meminfo', 'MemTotal:       32752640 kB')
    devs = [
        ('0000:00:00.0', '060000', '8086:591f', '106b:0163', 'skl_uncore'),
        ('0000:00:01.0', '060400', '8086:1901', '106b:0163', 'pcieport'),
        ('0000:00:14.0', '0c0330', '8086:a2af', '106b:0163', 'xhci_hcd'),
        ('0000:00:16.0', '078000', '8086:a2ba', '106b:0163', 'mei_me'),
        ('0000:00:17.0', '010601', '8086:a102', '106b:0163', 'ahci'),
        ('0000:00:1c.0', '060400', '8086:a290', '106b:0163', 'pcieport'),
        ('0000:00:1f.0', '060100', '8086:a2c5', '106b:0163', None),
        ('0000:00:1f.3', '040300', '8086:a2f0', '106b:0163', 'snd_hda_intel'),
        ('0000:00:1f.4', '0c0500', '8086:a2a3', '106b:0163', 'i801_smbus'),
        ('0000:01:00.0', '030000', '1002:67df', '106b:0163', 'amdgpu'),
        ('0000:01:00.1', '040300', '1002:aaf0', '106b:aaf0', 'snd_hda_intel'),
        ('0000:02:00.0', '028000', '14e4:43ba', '106b:0170', 'brcmfmac'),
        ('0000:03:00.0', '020000', '14e4:1686', '14e4:1686', 'tg3'),
        ('0000:03:00.1', '080501', '14e4:16bc', '14e4:16bc', 'sdhci-pci'),
        ('0000:04:00.0', '010802', '144d:a804', '144d:a801', 'nvme'),
        ('0000:05:00.0', '060400', '8086:15d3', '2222:1111', 'pcieport'),
        ('0000:07:00.0', '088000', '8086:15d2', '2222:1111', 'thunderbolt'),
        ('0000:3a:00.0', '0c0330', '8086:15d4', '2222:1111', 'xhci_hcd'),
    ]
    for slot, cls, vid, sub, drv in devs:
        pci(root, slot, cls, vid, sub, drv, boot_vga=(True if slot == '0000:01:00.0' else None))
    for name, vid, hub in [('usb1', '1d6b:0002', True), ('usb2', '1d6b:0003', True), ('1-5', '05ac:8296', False), ('1-6', '05ac:8511', False)]:
        usb(root, name, vid, hub=hub)
    usb(root, '1-1', '0951:1643', storage=True)
    drm(root, 'card1', '0000:01:00.0', pci_uevent(root, '0000:01:00.0'),
        [('DP-1', 'disconnected', []), ('DP-2', 'disconnected', []), ('DP-3', 'disconnected', []),
         ('eDP-1', 'connected', ['3840x2160', '3200x1800', '2560x1440', '1920x1200', '1920x1080', '1600x1200'])])
    w(root, '/sys/class/graphics/fb0/name', 'amdgpudrmfb')
    w(root, '/sys/class/graphics/fb0/virtual_size', '3840,2160')
    hwmon(root, 0, 'coretemp', [('Package id 0', 43000, 100000), ('Core 0', 42000, 100000), ('Core 1', 41000, 100000),
                                ('Core 2', 43000, 100000), ('Core 3', 40000, 100000)])
    hwmon(root, 1, 'amdgpu', [('edge', 47000, 104000)])
    smc = '/sys/bus/acpi/devices/APP0001:00/'
    for k, v in {'fan1_label': 'Main', 'fan1_input': '1200', 'fan1_min': '1200', 'fan1_max': '2700', 'light': '(3,0)'}.items():
        w(root, smc + k, v)
    w(root, '/sys/module/applesmc/refcnt', '0')
    kb = '/sys/class/power_supply/hid-F0TTESTSERIALKB-battery-144/'
    for k, v in {'type': 'Battery', 'scope': 'Device', 'present': '1', 'online': '1', 'status': 'Discharging', 'capacity': '85',
                 'model_name': 'Magic Keyboard with Numeric Keypad'}.items():
        w(root, kb + k, v)
    w(root, f'/usr/lib/modules/{kver}/modules.dep', 'kernel/drivers/hwmon/applesmc.ko.zst:')
    w(root, '/proc/asound/cards', ' 0 [PCH            ]: HDA-Intel - HDA Intel PCH\n 1 [HDMI           ]: HDA-Intel - HDA ATI HDMI')
    w(root, '/proc/asound/card0/codec#0', 'Codec: Cirrus Logic CS8409\nAddress: 0')
    w(root, '/proc/asound/card1/codec#0', 'Codec: ATI R6xx HDMI\nAddress: 0')
    lo_net(root)
    netdev(root, 'eth0', 'tg3', '0000:03:00.0', carrier=False, oper='down')
    netdev(root, 'wlan0', 'brcmfmac', '0000:02:00.0', wifi=True)
    block(root, 'sda', 'pci0000:00/0000:00:17.0/ata1/host0/target0:0:0/0:0:0:0', ['sda1', 'sda2'])
    block(root, 'nvme0n1', 'pci0000:00/0000:00:1c.0/0000:04:00.0/nvme/nvme0', ['nvme0n1p1', 'nvme0n1p2'], size=250069680)
    block(root, 'sdb', 'pci0000:00/0000:00:14.0/usb1/1-1/1-1:1.0/host6/target6:0:0/6:0:0:0', ['sdb1', 'sdb2'], removable=1, size=31277232)
    block(root, 'mmcblk0', 'pci0000:00/0000:00:1c.0/0000:03:00.1/mmc_host/mmc0/mmc0:aaaa', ['mmcblk0p1'], removable=0, size=62333952)
    kmsg(root, [
        (0.28, 6, 'pci 0000:01:00.0: vgaarb: setting as boot VGA device'),
        (6.20, 6, 'amdgpu 0000:01:00.0: initializing kernel modesetting (POLARIS10 0x1002:0x67DF 0x106B:0x0163 0xC5).'),
        (9.80, 3, 'brcmfmac: brcmf_c_process_clm_blob: no clm_blob available (err=-2), device may have limited channels available'),
    ])
    egl(root, [('/dev/dri/renderD128', 'AMD Radeon RX 470 Graphics (radeonsi, polaris10, ACO, DRM 3.64, 7.2.5-3-omarchy)', 'OpenGL ES 3.2 Mesa 26.2.2-arch1.1', 'AMD', 1626.0, 1490.2)])
    vblank(root, 'card1', 'eDP-1', '3840x2160', 60.0, 60, 1.0)


def qemu(root):
    """A QEMU guest (q35, UEFI, std VGA) like the image build's smoke test: not a Mac."""
    kver = '7.2.5-3-omarchy'
    common(root, kver, 'BOOT_IMAGE=/arch/boot/x86_64/vmlinuz-linux-omarchy archisobasedir=arch archisosearchuuid=2026-10-10-03-12-40-00 quiet')
    dmi = '/sys/class/dmi/id/'
    for k, v in {'product_name': 'Standard PC (Q35 + ICH9, 2009)', 'board_name': '', 'sys_vendor': 'QEMU', 'bios_version': 'unknown', 'chassis_type': '1'}.items():
        w(root, dmi + k, v)
    w(root, '/proc/cpuinfo', 'processor\t: 0\nmodel name\t: QEMU Virtual CPU version 2.5+\n\nprocessor\t: 1\nmodel name\t: QEMU Virtual CPU version 2.5+\n')
    w(root, '/proc/meminfo', 'MemTotal:        4030000 kB')
    for slot, cls, vid, sub, drv in [('0000:00:00.0', '060000', '8086:29c0', '1af4:1100', None), ('0000:00:01.0', '030000', '1234:1111', '1af4:1100', 'bochs-drm'),
                                     ('0000:00:02.0', '0c0330', '1b36:000d', '1af4:1100', 'xhci_hcd'), ('0000:00:1f.2', '010601', '8086:2922', '1af4:1100', 'ahci')]:
        pci(root, slot, cls, vid, sub, drv, boot_vga=(True if slot == '0000:00:01.0' else None))
    usb(root, 'usb1', '1d6b:0002', hub=True)
    usb(root, '1-1', '46f4:0001', storage=True)
    usb(root, '1-2', '46f4:0001', storage=True)
    lo_net(root)
    netdev(root, 'eth0', 'virtio_net', None, carrier=True, oper='up')
    w(root, '/proc/net/route', 'Iface\tDestination\tGateway \tFlags\tRefCnt\tUse\tMetric\tMask\t\tMTU\tWindow\tIRTT\neth0\t00000000\t0202000A\t0003\t0\t0\t1024\t00000000\t0\t0\t0')
    block(root, 'sda', 'pci0000:00/0000:00:02.0/usb1/1-1/1-1:1.0/host0/target0:0:0/0:0:0:0', ['sda1', 'sda2'], removable=1)
    block(root, 'sdb', 'pci0000:00/0000:00:02.0/usb1/1-2/1-2:1.0/host1/target1:0:0/1:0:0:0', ['sdb1'], removable=1, size=131072)
    ln(root, '/dev/disk/by-label/BOOTLIVE', '../../sdb1')
    kmsg(root, [(0.5, 6, '[drm] Initialized bochs-drm 1.0.0 for 0000:00:01.0 on minor 0')])
    drm(root, 'card0', '0000:00:01.0', pci_uevent(root, '0000:00:01.0'), [('Virtual-1', 'connected', ['1280x800', '1024x768'])], render=None)


for name, fn in [('macbookair5,2', mac_air), ('imac10,1', mac_imac10), ('imac18,3', mac_imac18), ('qemu', qemu)]:
    root = os.path.join(OUT, name)
    if os.path.isdir(root) and name:
        shutil.rmtree(root)
    os.makedirs(root)
    fn(root)
    print('wrote', root)
