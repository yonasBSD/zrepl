// Code generated by "enumer -type NoFitBehavior"; DO NOT EDIT.

//
package base2bufpool

import (
	"fmt"
)

const _NoFitBehaviorName = "PanicAllocateSmallerAllocateLargerAllocate"

var _NoFitBehaviorIndex = [...]uint8{0, 5, 20, 34, 42}

func (i NoFitBehavior) String() string {
	if i >= NoFitBehavior(len(_NoFitBehaviorIndex)-1) {
		return fmt.Sprintf("NoFitBehavior(%d)", i)
	}
	return _NoFitBehaviorName[_NoFitBehaviorIndex[i]:_NoFitBehaviorIndex[i+1]]
}

var _NoFitBehaviorValues = []NoFitBehavior{0, 1, 2, 3}

var _NoFitBehaviorNameToValueMap = map[string]NoFitBehavior{
	_NoFitBehaviorName[0:5]:   0,
	_NoFitBehaviorName[5:20]:  1,
	_NoFitBehaviorName[20:34]: 2,
	_NoFitBehaviorName[34:42]: 3,
}

// NoFitBehaviorString retrieves an enum value from the enum constants string name.
// Throws an error if the param is not part of the enum.
func NoFitBehaviorString(s string) (NoFitBehavior, error) {
	if val, ok := _NoFitBehaviorNameToValueMap[s]; ok {
		return val, nil
	}
	return 0, fmt.Errorf("%s does not belong to NoFitBehavior values", s)
}

// NoFitBehaviorValues returns all values of the enum
func NoFitBehaviorValues() []NoFitBehavior {
	return _NoFitBehaviorValues
}

// IsANoFitBehavior returns "true" if the value is listed in the enum definition. "false" otherwise
func (i NoFitBehavior) IsANoFitBehavior() bool {
	for _, v := range _NoFitBehaviorValues {
		if i == v {
			return true
		}
	}
	return false
}