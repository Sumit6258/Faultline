// Package match solves the assignment problem: given riders waiting and drivers
// free, and what each pairing would cost, who goes with whom.
//
// Picking the nearest driver for each rider in turn is cheap and wrong in a
// specific way. The first rider takes the driver the second rider could only
// have had, and the second waits for one far away. Looking at everyone at once
// gives up a few seconds of waiting for a better overall result, and whether
// that trade is worth it is the question the simulation in this repository
// measures.
package match

import "math"

// Forbidden marks a pairing that is not allowed, such as a driver too far away to be useful.
var Forbidden = math.Inf(1)

// Greedy goes through the rows in order and gives each the cheapest column not yet taken. assigned[i] is the
// column for row i, or -1.
func Greedy(cost [][]float64) []int {
	assigned := make([]int, len(cost))
	var cols int
	if len(cost) > 0 {
		cols = len(cost[0])
	}
	used := make([]bool, cols)
	for i, row := range cost {
		assigned[i] = -1
		best := -1
		for j, c := range row {
			if used[j] || math.IsInf(c, 1) {
				continue
			}
			if best < 0 || c < row[best] {
				best = j
			}
		}
		if best >= 0 {
			assigned[i], used[best] = best, true
		}
	}
	return assigned
}

// Optimal pairs rows with columns so that as many rows as possible get a column, and among all ways to
// match that many, the total cost is smallest. It is the Hungarian algorithm. With n rows and m columns it
// takes O(n * n * m) when n is the smaller side, and when there are more rows than columns it solves the transposed
// problem instead. Padding the matrix to a square would cost the cube of the larger side, which for 150 riders
// and the 1,200 drivers they can reach is about 60 times more work. assigned[i] is the column for row i, or -1.
func Optimal(cost [][]float64) []int {
	rows := len(cost)
	assigned := make([]int, rows)
	for i := range assigned {
		assigned[i] = -1
	}
	if rows == 0 || len(cost[0]) == 0 {
		return assigned
	}
	cols := len(cost[0])
	if rows <= cols {
		for i, j := range hungarian(cost, rows, cols, func(i, j int) float64 { return cost[i][j] }) {
			assigned[i] = j
		}
		return assigned
	}
	// More rows than columns: solve with the roles swapped, so every column gets a row, and map back.
	for j, i := range hungarian(cost, cols, rows, func(j, i int) float64 { return cost[i][j] }) {
		if i >= 0 {
			assigned[i] = j
		}
	}
	return assigned
}

// hungarian solves the problem for n rows and m columns, n <= m, where at(i, j) is the cost of pairing row i
// with column j, and returns for each row its column or -1 if it could only have been paired with a forbidden one.
func hungarian(_ [][]float64, n, m int, at func(i, j int) float64) []int {
	// Forbidden pairs cost more than any combination of real ones, so the algorithm uses one only when it
	// has no real pair left, and that is what makes it match as many as possible before minimising the cost.
	var maxFinite float64
	for i := 0; i < n; i++ {
		for j := 0; j < m; j++ {
			if c := at(i, j); !math.IsInf(c, 1) && c > maxFinite {
				maxFinite = c
			}
		}
	}
	big := float64(n)*maxFinite + 1
	a := make([][]float64, n+1)
	for i := 1; i <= n; i++ {
		a[i] = make([]float64, m+1)
		for j := 1; j <= m; j++ {
			if c := at(i-1, j-1); !math.IsInf(c, 1) {
				a[i][j] = c
			} else {
				a[i][j] = big
			}
		}
	}

	u, v := make([]float64, n+1), make([]float64, m+1)
	p, way := make([]int, m+1), make([]int, m+1)
	minv := make([]float64, m+1)
	used := make([]bool, m+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		for j := range minv {
			minv[j], used[j] = math.Inf(1), false
		}
		for {
			used[j0] = true
			i0, delta, j1 := p[j0], math.Inf(1), 0
			for j := 1; j <= m; j++ {
				if used[j] {
					continue
				}
				if cur := a[i0][j] - u[i0] - v[j]; cur < minv[j] {
					minv[j], way[j] = cur, j0
				}
				if minv[j] < delta {
					delta, j1 = minv[j], j
				}
			}
			for j := 0; j <= m; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for j0 != 0 {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
		}
	}
	out := make([]int, n)
	for i := range out {
		out[i] = -1
	}
	for j := 1; j <= m; j++ {
		if i := p[j]; i >= 1 && !math.IsInf(at(i-1, j-1), 1) {
			out[i-1] = j - 1
		}
	}
	return out
}

// Total is the summed cost of an assignment and how many rows it matched.
func Total(cost [][]float64, assigned []int) (total float64, matched int) {
	for i, j := range assigned {
		if j >= 0 {
			total += cost[i][j]
			matched++
		}
	}
	return
}
