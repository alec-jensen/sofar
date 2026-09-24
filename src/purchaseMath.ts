export function purchaseImpact(
  price: number,
  monthlyAllowance: number,
  spentThisMonth: number,
  daysInMonth: number,
) {
  const roomBefore = monthlyAllowance - spentThisMonth;
  return {
    shareOfAllowance:
      monthlyAllowance > 0 ? (price / monthlyAllowance) * 100 : null,
    daysOfAllowance:
      monthlyAllowance > 0 && daysInMonth > 0
        ? price / (monthlyAllowance / daysInMonth)
        : null,
    roomBefore,
    roomAfter: roomBefore - price,
  };
}

export function investmentProjection(
  amount: number,
  annualReturnPercent: number,
  years: number,
) {
  const projected = Math.round(
    amount * (1 + annualReturnPercent / 100) ** years,
  );
  return {
    projected,
    growth: projected - amount,
  };
}
