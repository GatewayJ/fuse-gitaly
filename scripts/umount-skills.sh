#!/bin/bash
# 清理 skills FUSE 残留挂载点，便于重新挂载
MOUNT_POINT="${1:-/root/.agentichub/skills}"
if mountpoint -q "$MOUNT_POINT" 2>/dev/null; then
  echo "Unmounting $MOUNT_POINT..."
  fusermount -u "$MOUNT_POINT" 2>/dev/null || sudo fusermount -u "$MOUNT_POINT" 2>/dev/null || sudo umount "$MOUNT_POINT" 2>/dev/null
  echo "Done."
else
  echo "Not a mount point (or stale). Trying to force unmount..."
  fusermount -uz "$MOUNT_POINT" 2>/dev/null || sudo fusermount -uz "$MOUNT_POINT" 2>/dev/null || sudo umount -l "$MOUNT_POINT" 2>/dev/null
  if [ -d "$MOUNT_POINT" ]; then
    if rmdir "$MOUNT_POINT" 2>/dev/null; then
      echo "Removed stale directory. Recreate with: mkdir -p $MOUNT_POINT"
    else
      echo "Directory still exists. If 'Transport endpoint is not connected' persists, run: sudo umount -l $MOUNT_POINT && rmdir $MOUNT_POINT && mkdir -p $MOUNT_POINT"
    fi
  fi
fi
