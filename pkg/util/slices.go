package util

import (
	"reflect"
)

// CopyInts copies a slice of ints.
func CopyInts(input []int) []int {
	results := make([]int, len(input))
	copy(results, input)
	return results
}

// SameElements determines whether two int slices have the
// same elements (in any order).
func SameElements(slice1 []int, slice2 []int) bool {
	if len(slice1) != len(slice2) {
		return false
	}

	slice1Counts := map[int]int{}
	for _, s := range slice1 {
		slice1Counts[s]++
	}

	slice2Counts := map[int]int{}
	for _, s := range slice2 {
		slice2Counts[s]++
	}

	return reflect.DeepEqual(slice1Counts, slice2Counts)
}

// Returns a slice containing elements from slice1 but not in slice2
func Difference[T comparable](slice1, slice2 []T) []T {
	diff := []T{}
    for _, item1 := range slice1 {
        found := false
        for _, item2 := range slice2 {
            if item1 == item2 {
                found = true
                break
            }
        }
        if !found {
            diff = append(diff, item1)
        }
    }
    return diff	
}
