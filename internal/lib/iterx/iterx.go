package iterx

import "iter"

func Collect[T any](it iter.Seq2[T, error]) ([]T, error) {
	s := make([]T, 0)

	for t, err := range it {
		if err != nil {
			return nil, err
		}

		s = append(s, t)
	}

	return s, nil
}
