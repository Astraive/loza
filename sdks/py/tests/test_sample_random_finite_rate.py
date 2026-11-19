from __future__ import annotations

import pytest

import loza
from loza.core.sampler import sample_random as core_sample_random
from loza.sampling import sample_random as sampling_sample_random


@pytest.mark.parametrize("factory", [sampling_sample_random, core_sample_random, loza.sample_random])
@pytest.mark.parametrize("rate", [float("nan"), float("inf"), float("-inf")])
def test_sample_random_rejects_non_finite_rates(factory, rate):
    with pytest.raises(ValueError, match="must be finite"):
        factory(rate)


@pytest.mark.parametrize("factory", [sampling_sample_random, core_sample_random, loza.sample_random])
def test_sample_random_still_clamps_finite_rates(factory):
    assert factory(-1.0)(object()) is False
    assert factory(2.0)(object()) is True
