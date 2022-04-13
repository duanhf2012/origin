package buildtime

/*
#include <stdio.h>

#define OS_YEAR     ((((__DATE__ [7] - '0') * 10 + (__DATE__ [8] - '0')) * 10 \
                                    + (__DATE__ [9] - '0')) * 10 + (__DATE__ [10] - '0'))

// Retrieve month info
#define OS_MONTH    (__DATE__ [2] == 'n' ? (__DATE__ [1] == 'a' ? 1 : 6) \
                                : __DATE__ [2] == 'b' ? 2 \
                                : __DATE__ [2] == 'r' ? (__DATE__ [0] == 'M' ? 3 : 4) \
                                : __DATE__ [2] == 'y' ? 5 \
                                : __DATE__ [2] == 'l' ? 7 \
                                : __DATE__ [2] == 'g' ? 8 \
                                : __DATE__ [2] == 'p' ? 9 \
                                : __DATE__ [2] == 't' ? 10 \
                                : __DATE__ [2] == 'v' ? 11 : 12)

// Retrieve day info
#define OS_DAY      ((__DATE__ [4] == ' ' ? 0 : __DATE__ [4] - '0') * 10 \
                                + (__DATE__ [5] - '0'))

// Retrieve hour info
#define OS_HOUR     ((__TIME__ [0] - '0') * 10 + (__TIME__ [1] - '0'))

// Retrieve minute info
#define OS_MINUTE   ((__TIME__ [3] - '0') * 10 + (__TIME__ [4] - '0'))

// Retrieve second info
#define OS_SECOND   ((__TIME__ [6] - '0') * 10 + (__TIME__ [7] - '0'))


char* GetBuildTime()
{
    static char buffer[16] = {0};
	snprintf(buffer, 16,"%04d%02d%02d%02d%02d%02d", OS_YEAR, OS_MONTH, OS_DAY, OS_HOUR, OS_MINUTE, OS_SECOND);
    return buffer;
}
*/
import "C"

func GetBuildDateTime() string {
	return C.GoString(C.GetBuildTime())
}
