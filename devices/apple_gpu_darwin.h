#ifndef GOTOP_APPLE_GPU_DARWIN_H
#define GOTOP_APPLE_GPU_DARWIN_H

#include <stdint.h>

struct apple_gpu_info {
  char *name;
  uint64_t total_mem;
  uint64_t used_mem;
  int64_t gpu_util;
};

int apple_gpu_get_infos(struct apple_gpu_info **out_infos, int *out_count, char **out_err);
void apple_gpu_free_infos(struct apple_gpu_info *infos, int count);
void apple_gpu_free_error(char *err);

#endif
