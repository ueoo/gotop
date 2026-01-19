#import <Foundation/Foundation.h>
#import <Metal/Metal.h>
#import <IOKit/IOKitLib.h>
#include <CoreFoundation/CoreFoundation.h>
#include <mach/mach.h>
#include <stdbool.h>
#include <stdlib.h>
#include <string.h>

#include "apple_gpu_darwin.h"

static uint64_t apple_gpu_total_system_memory(void) {
  mach_msg_type_number_t host_size = HOST_BASIC_INFO_COUNT;
  host_basic_info_data_t info;
  if (host_info(mach_host_self(), HOST_BASIC_INFO, (host_info_t)&info, &host_size) == KERN_SUCCESS) {
    return info.max_mem;
  }
  return 0;
}

static bool apple_gpu_cfnumber_to_int64(CFTypeRef value, int64_t *out) {
  if (value == NULL || CFGetTypeID(value) != CFNumberGetTypeID()) {
    return false;
  }
  return CFNumberGetValue((CFNumberRef)value, kCFNumberSInt64Type, out);
}

int apple_gpu_get_infos(struct apple_gpu_info **out_infos, int *out_count, char **out_err) {
  if (out_infos == NULL || out_count == NULL) {
    if (out_err != NULL) {
      *out_err = strdup("Apple GPU error: invalid output arguments");
    }
    return -1;
  }

  @autoreleasepool {
    NSArray<id<MTLDevice>> *mtl_devices = MTLCopyAllDevices();
    if (mtl_devices == nil) {
      if (out_err != NULL) {
        *out_err = strdup("Apple GPU error: failed to enumerate Metal devices");
      }
      return -1;
    }

    NSUInteger count = [mtl_devices count];
    struct apple_gpu_info *infos = calloc(count, sizeof(*infos));
    if (infos == NULL) {
      if (out_err != NULL) {
        *out_err = strdup("Apple GPU error: failed to allocate device list");
      }
      [mtl_devices release];
      return -1;
    }

    for (NSUInteger i = 0; i < count; ++i) {
      id<MTLDevice> dev = mtl_devices[i];
      const char *name = [[dev name] UTF8String];
      infos[i].name = strdup(name ? name : "Apple GPU");
      infos[i].gpu_util = -1;
      infos[i].total_mem = 0;
      infos[i].used_mem = 0;

      io_service_t gpu_service = IOServiceGetMatchingService(kIOMainPortDefault,
                                                             IORegistryEntryIDMatching([dev registryID]));
      if (MACH_PORT_VALID(gpu_service)) {
        CFMutableDictionaryRef cf_props = NULL;
        if (IORegistryEntryCreateCFProperties(gpu_service,
                                              &cf_props,
                                              kCFAllocatorDefault,
                                              kNilOptions) == kIOReturnSuccess) {
          CFTypeRef perf_stats = CFDictionaryGetValue(cf_props, CFSTR("PerformanceStatistics"));
          if (perf_stats != NULL && CFGetTypeID(perf_stats) == CFDictionaryGetTypeID()) {
            int64_t util_value = 0;
            if (apple_gpu_cfnumber_to_int64(
                    CFDictionaryGetValue((CFDictionaryRef)perf_stats, CFSTR("Device Utilization %")),
                    &util_value)) {
              infos[i].gpu_util = util_value;
            }

            if ([dev hasUnifiedMemory]) {
              int64_t used_value = 0;
              if (apple_gpu_cfnumber_to_int64(
                      CFDictionaryGetValue((CFDictionaryRef)perf_stats, CFSTR("Alloc system memory")),
                      &used_value)) {
                if (used_value > 0) {
                  infos[i].used_mem = (uint64_t)used_value;
                }
              }
            }
          }
          CFRelease(cf_props);
        }
        IOObjectRelease(gpu_service);
      }

      if ([dev hasUnifiedMemory]) {
        infos[i].total_mem = apple_gpu_total_system_memory();
      } else {
        infos[i].total_mem = [dev recommendedMaxWorkingSetSize];
      }
    }

    [mtl_devices release];
    *out_infos = infos;
    *out_count = (int)count;
    return 0;
  }
}

void apple_gpu_free_infos(struct apple_gpu_info *infos, int count) {
  if (infos == NULL) {
    return;
  }
  for (int i = 0; i < count; ++i) {
    free(infos[i].name);
  }
  free(infos);
}

void apple_gpu_free_error(char *err) {
  if (err != NULL) {
    free(err);
  }
}
