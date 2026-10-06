package reimburse

import "math"

// hungarian solves the rectangular assignment problem: it returns, for each row,
// the column it is assigned to, minimizing the total cost. rows <= cols is
// required (pad with dummy columns to allow "no match"). O(n^2 m).
func hungarian(cost [][]float64) []int {
	n := len(cost)
	if n == 0 {
		return nil
	}
	m := len(cost[0])
	const inf = math.MaxFloat64 / 4
	u := make([]float64, n+1)
	v := make([]float64, m+1)
	p := make([]int, m+1)
	way := make([]int, m+1)
	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, m+1)
		used := make([]bool, m+1)
		for j := range minv {
			minv[j] = inf
		}
		for {
			used[j0] = true
			i0, delta, j1 := p[j0], inf, 0
			for j := 1; j <= m; j++ {
				if used[j] {
					continue
				}
				cur := cost[i0-1][j-1] - u[i0] - v[j]
				if cur < minv[j] {
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
		for {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
			if j0 == 0 {
				break
			}
		}
	}
	out := make([]int, n)
	for j := 1; j <= m; j++ {
		if p[j] != 0 {
			out[p[j]-1] = j - 1
		}
	}
	return out
}

// Assignment is one chosen pair of the global solve.
type Assignment struct {
	Payment, Candidate int // indexes; Candidate is -1 for "no match"
}

// Assign solves the whole open set at once (S12b step 3): profit[i][j] is the log-odds
// of payment i being for candidate j over the decision threshold, or a very
// negative number when the pair is not allowed. Each candidate takes at most one
// payment; a payment may take none. Only pairs with positive profit are chosen.
func Assign(profit [][]float64) []Assignment {
	n := len(profit)
	if n == 0 {
		return nil
	}
	m := 0
	for _, row := range profit {
		if len(row) > m {
			m = len(row)
		}
	}
	// columns: real candidates, then one "abstain" column per payment
	cost := make([][]float64, n)
	for i := range cost {
		cost[i] = make([]float64, m+n)
		for j := 0; j < m; j++ {
			c := 1e6
			if j < len(profit[i]) && profit[i][j] > 0 {
				c = -profit[i][j]
			}
			cost[i][j] = c
		}
		for j := m; j < m+n; j++ {
			cost[i][j] = 0
		}
	}
	cols := hungarian(cost)
	out := make([]Assignment, n)
	for i, j := range cols {
		out[i] = Assignment{Payment: i, Candidate: -1}
		if j < m && j < len(profit[i]) && profit[i][j] > 0 {
			out[i].Candidate = j
		}
	}
	return out
}
