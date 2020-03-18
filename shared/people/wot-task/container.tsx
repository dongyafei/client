import WotTask from '.'
import * as PeopleGen from '../../actions/people-gen'
import * as RPCTypes from '../../constants/types/rpc-gen'
import * as Container from '../../util/container'

type OwnProps = {
  key: string
  onClickUser: (username: string) => void
  status: RPCTypes.WotStatusType
  vouchee: string
  voucher: string
}

const mapStateToProps = state => ({myUsername: state.config.username || ''})

const mapDispatchToProps = dispatch => ({
  onDismiss: (voucher: string, vouchee: string) => {
    dispatch(PeopleGen.createDismissWotNotifications({vouchee, voucher}))
  },
})

const mergeProps = (stateProps, dispatchProps, ownProps: OwnProps) => {
  const otherUser =
    stateProps.myUsername.localeCompare(ownProps.voucher) === 0 ? ownProps.vouchee : ownProps.voucher
  return {
    ...dispatchProps,
    badged: true,
    key: ownProps.key,
    onClickUser: ownProps.onClickUser,
    otherUser: otherUser,
    userForIcon: otherUser,
    vouchee: ownProps.vouchee,
    voucher: ownProps.voucher,
    wotStatus: ownProps.status,
  }
}

export default Container.connect(mapStateToProps, mapDispatchToProps, mergeProps)(WotTask)
