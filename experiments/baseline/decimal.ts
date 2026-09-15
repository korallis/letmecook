// Arithmetic over the canonical decimal value of each already-parsed JSON number.
// No epsilon, fixed decimal-place limit, or rounding enters sums/comparisons.
export type Decimal = { units: bigint; scale: number };

function decimal(value: number): Decimal {
  const [significand, exponent = '0'] = String(value).split('e');
  const [whole, fraction = ''] = significand.split('.');
  const scale = fraction.length - Number(exponent), units = BigInt(whole + fraction);
  return scale < 0 ? { units: units * 10n ** BigInt(-scale), scale: 0 } : { units, scale };
}

export function addDecimals(values: Decimal[]): Decimal {
  const scale = values.reduce((maximum, value) => Math.max(maximum, value.scale), 0);
  return { units: values.reduce((sum, value) => sum + value.units * 10n ** BigInt(scale - value.scale), 0n), scale };
}

export const sumDecimals = (values: number[]) => addDecimals(values.map(decimal));

export function decimalText(value: Decimal): string {
  const digits = value.units.toString().padStart(value.scale + 1, '0');
  if (value.scale === 0) return digits;
  return (digits.slice(0, -value.scale) + '.' + digits.slice(-value.scale)).replace(/\.?0+$/, '');
}

// Numeric fields are projections only; exact text remains available when a sum
// or a positive difference cannot be represented by a JSON/JavaScript number.
export const decimalNumber = (value: Decimal) => Number(decimalText(value));

export function decimalExcess(value: Decimal, maximum: number): Decimal {
  const cap = decimal(maximum), difference = addDecimals([value, { ...cap, units: -cap.units }]);
  return { ...difference, units: difference.units > 0n ? difference.units : 0n };
}
