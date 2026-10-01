package monitoring

import (
	"testing"

	"github.com/shirou/gopsutil/v4/disk"
)

func TestIsPhysicalDisk(t *testing.T) {
	tests := []struct {
		name string
		part disk.PartitionStat
		want bool
	}{
		{"android sdcard", disk.PartitionStat{Device: "/dev/fuse", Mountpoint: "/sdcard", Fstype: "fuse"}, true},
		{"case-insensitive filesystem", disk.PartitionStat{Device: "/dev/fuse", Mountpoint: "/sdcard", Fstype: "FUSE"}, true},
		{"other fuse mount", disk.PartitionStat{Device: "/dev/fuse", Mountpoint: "/data", Fstype: "fuse"}, false},
		{"nested sdcard mount", disk.PartitionStat{Device: "/dev/fuse", Mountpoint: "/sdcard/nested", Fstype: "fuse"}, false},
		{"other fuse device", disk.PartitionStat{Device: "sshfs", Mountpoint: "/sdcard", Fstype: "fuse"}, false},
		{"fuse subtype", disk.PartitionStat{Device: "/dev/fuse", Mountpoint: "/sdcard", Fstype: "fuse.sshfs"}, false},
		{"nullfs", disk.PartitionStat{Device: "/dev/disk1", Mountpoint: "/Users/shared", Fstype: "nullfs"}, false},
		{"nullfs subtype", disk.PartitionStat{Device: "/dev/disk1", Mountpoint: "/Users/shared", Fstype: "NULLFS.other"}, false},
		{"root remains included", disk.PartitionStat{Device: "/dev/loop0", Mountpoint: "/", Fstype: "overlay"}, true},
		{"nullfs root remains included", disk.PartitionStat{Device: "root", Mountpoint: "/", Fstype: "nullfs"}, true},
		{"autofs trigger", disk.PartitionStat{Device: "map", Mountpoint: "/data", Fstype: "autofs"}, false},
		{"device autofs", disk.PartitionStat{Device: "/dev/sda1", Mountpoint: "/data", Fstype: "autofs"}, true},
		{"physical fuseblk", disk.PartitionStat{Device: "/dev/sda1", Mountpoint: "/data", Fstype: "fuseblk"}, true},
		{"temporary filesystem", disk.PartitionStat{Device: "tmpfs", Mountpoint: "/data", Fstype: "tmpfs"}, false},
		{"excluded mountpoint", disk.PartitionStat{Device: "/dev/sda1", Mountpoint: "/var/lib/docker", Fstype: "ext4"}, false},
		{"loop device", disk.PartitionStat{Device: "/dev/loop1", Mountpoint: "/data", Fstype: "ext4"}, false},
		{"remote option", disk.PartitionStat{Device: "Z:", Mountpoint: "Z:", Fstype: "ntfs", Opts: []string{"rw", "remote"}}, false},
		{"network filesystem", disk.PartitionStat{Device: "host:/share", Mountpoint: "/data", Fstype: "nfs4"}, false},
		{"local filesystem", disk.PartitionStat{Device: "/dev/sda1", Mountpoint: "/data", Fstype: "ext4"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPhysicalDisk(tt.part); got != tt.want {
				t.Fatalf("isPhysicalDisk(%+v) = %v, want %v", tt.part, got, tt.want)
			}
		})
	}
}
